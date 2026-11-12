package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// VerificationURL starts a temporary loopback helper for the pending challenge.
// Only the native wrapper opens this URL; no management API is exposed on it.
func (s *Service) VerificationURL() (string, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	p, err := s.pendingAt("verification")
	if err != nil {
		return "", err
	}
	if p.challenge == nil || !slices.Contains(p.challenge.HumanVerificationMethods, "captcha") {
		return "", errors.New("this verification method is not supported; cancel and try signing in again")
	}
	if p.verifyURL != "" {
		return p.verifyURL, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", errors.New("could not open the local verification helper")
	}
	nonce := make([]byte, 24)
	if _, err := rand.Read(nonce); err != nil {
		listener.Close()
		return "", err
	}
	secretPath := "/" + hex.EncodeToString(nonce)
	origin := "http://" + listener.Addr().String()
	challengeURL := "https://verify-api.proton.me/core/v4/captcha?Token=" + url.QueryEscape(p.challenge.HumanVerificationToken) + "&ForceWebMessaging=1&Dark=true"
	challengeJSON, _ := json.Marshal(challengeURL)
	page := strings.ReplaceAll(verificationHTML, "{{NONCE}}", hex.EncodeToString(nonce))
	page = strings.ReplaceAll(page, "{{CHALLENGE}}", string(challengeJSON))
	mux := http.NewServeMux()
	tokens := make(chan string, 1)
	mux.HandleFunc("GET "+secretPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf("default-src 'none'; script-src 'nonce-%s'; style-src 'nonce-%s'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'", hex.EncodeToString(nonce), hex.EncodeToString(nonce)))
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("POST "+secretPath+"/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != origin {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		var data struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&data); err != nil || data.Token == "" || len(data.Token) > 8192 {
			http.Error(w, "invalid token", http.StatusBadRequest)
			return
		}
		select {
		case tokens <- data.Token:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	p.verifyURL = origin + secretPath
	go func() { _ = srv.Serve(listener) }()
	go func() {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()
		select {
		case <-p.ctx.Done():
			return
		case token := <-tokens:
			s.opMu.Lock()
			defer s.opMu.Unlock()
			current, err := s.pendingAt("verification")
			if err != nil || current != p {
				return
			}
			p.client.SetHumanVerification(token, "captcha")
			p.verifyURL = ""
			if _, err := s.attemptLogin(p); err != nil {
				s.mu.Lock()
				s.lastError = err.Error()
				s.mu.Unlock()
			}
		}
	}()
	return p.verifyURL, nil
}

const verificationHTML = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>iOxide verification</title>
<style nonce="{{NONCE}}">body{font:16px system-ui;background:#1b1c24;color:#f4f4fa;max-width:540px;margin:12vh auto;padding:24px}button,input{font:inherit;padding:12px;border-radius:8px;border:1px solid #65657b}button{background:#9890ff;color:#15151d;cursor:pointer}input{box-sizing:border-box;width:100%;background:#252631;color:#fff;margin:12px 0}p{line-height:1.6}details{margin-top:32px}summary{cursor:pointer}#result{color:#69daee}</style></head><body>
<h1>Verify your sign-in</h1><p>Complete the challenge in the window opened by the button below. Then return to iOxide.</p><button id="open">Open verification challenge</button><p id="result" role="status"></p>
<details><summary>Verification did not return automatically?</summary><p>If you already obtained a verification token from the challenge, paste it below. Otherwise close this tab and retry sign-in in iOxide.</p><label for="token">Verification token</label><input id="token" type="password" autocomplete="off"><button id="submit">Submit token</button></details>
<script nonce="{{NONCE}}">
const challengeURL={{CHALLENGE}};let challenge;
document.getElementById('open').addEventListener('click',()=>{challenge=window.open(challengeURL,'ioxide-verify','width=480,height=680');document.getElementById('result').textContent='Waiting for verification…'});
async function submit(token){if(!token)return;const response=await fetch(location.pathname+'/token',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token})});if(response.ok){document.getElementById('token').value='';document.getElementById('result').textContent='Verification received. Return to iOxide.'}else{document.getElementById('result').textContent='Verification could not be submitted. Return to iOxide and retry.'}}
window.addEventListener('message',event=>{if(event.origin!=='https://verify-api.proton.me'||event.source!==challenge)return;const d=event.data;const token=d&&(d.token||d.payload?.token||d.data?.token);if(typeof token==='string')submit(token).catch(()=>{document.getElementById('result').textContent='Return to iOxide to continue.'})});
document.getElementById('submit').addEventListener('click',()=>submit(document.getElementById('token').value).catch(()=>{document.getElementById('result').textContent='The verification helper has expired. Return to iOxide.'}));
</script></body></html>`
