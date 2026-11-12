package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type NetworkStatus struct {
	Installed  bool   `json:"installed"`
	Running    bool   `json:"running"`
	HTTPSReady bool   `json:"httpsReady"`
	URL        string `json:"url"`
	Message    string `json:"message"`
}

func (d *Desktop) TailscaleStatus() NetworkStatus {
	result := NetworkStatus{Message: "Install Tailscale on this computer and your other device, then sign in to both."}
	path, err := exec.LookPath("tailscale")
	if err != nil {
		candidates := []string{}
		if runtime.GOOS == "windows" && os.Getenv("ProgramFiles") != "" {
			candidates = append(candidates, filepath.Join(os.Getenv("ProgramFiles"), "Tailscale", "tailscale.exe"))
		}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates, "/Applications/Tailscale.app/Contents/MacOS/Tailscale")
		}
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return result
	}
	result.Installed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, path, "status", "--json").Output()
	var status struct {
		BackendState string
		Self         struct{ DNSName string }
	}
	if err != nil || json.Unmarshal(data, &status) != nil || status.BackendState != "Running" {
		result.Message = "Tailscale is installed. Open Tailscale and connect to your network."
		return result
	}
	result.Running = true
	host := strings.TrimSuffix(status.Self.DNSName, ".")
	if host == "" {
		result.Message = "Enable MagicDNS and HTTPS certificates in your Tailscale admin console."
		return result
	}
	result.URL = "https://" + host + "/caldav/"
	result.Message = "Tailscale is connected. Configure HTTPS forwarding to localhost port 8081."
	data, err = exec.CommandContext(ctx, path, "serve", "status", "--json").Output()
	var config struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string }
		}
	}
	if err == nil && json.Unmarshal(data, &config) == nil {
		if web, ok := config.Web[host+":443"]; ok {
			proxy := web.Handlers["/"].Proxy
			if proxy == "http://127.0.0.1:8081" || proxy == "http://localhost:8081" || proxy == "http://127.0.0.1:8081/" {
				result.HTTPSReady = true
				result.Message = "HTTPS forwarding is configured. Connect your other device to the same Tailscale network."
			}
		}
	}
	return result
}

func validateConnectionURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("enter an HTTPS server address without credentials, query parameters or a fragment")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/caldav/"
	}
	if u.Path != "/caldav/" || len(raw) > 2048 {
		return "", errors.New("the server address must end with /caldav/")
	}
	return u.String(), nil
}

func preferencesPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "ioxide", "desktop.json"), nil
}

func (d *Desktop) ConnectionURL() string {
	path, err := preferencesPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var prefs struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(data, &prefs) != nil {
		return ""
	}
	value, _ := validateConnectionURL(prefs.URL)
	return value
}

func (d *Desktop) SaveConnectionURL(raw string) error {
	value := ""
	var err error
	if raw != "" {
		value, err = validateConnectionURL(raw)
		if err != nil {
			return err
		}
	}
	path, err := preferencesPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		URL string `json:"url"`
	}{value})
	f, err := os.CreateTemp(filepath.Dir(path), ".desktop-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// CheckConnection probes TLS and unauthenticated CalDAV readiness only. It never
// forwards a bridge password to a user-entered address or follows redirects.
func (d *Desktop) CheckConnection(raw string) error {
	value, err := validateConnectionURL(raw)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(value)
	if err != nil {
		return errors.New("could not reach this HTTPS address; check Tailscale, the hostname and the certificate")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") != `Basic realm="ioxide"` {
		return errors.New("the address is reachable but did not respond like the iOxide authentication endpoint")
	}
	return nil
}
