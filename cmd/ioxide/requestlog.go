package main

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// statusRecorder remembers the status code written to a response.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	errMsg []byte // start of the body of a failed response
}

const errMsgLimit = 300

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	if r.status >= 400 && len(r.errMsg) < errMsgLimit {
		r.errMsg = append(r.errMsg, b[:min(len(b), errMsgLimit-len(r.errMsg))]...)
	}
	return n, err
}

// logRequests logs one line per request: method, path, status, size and
// duration. Credentials are never logged.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 && len(rec.errMsg) > 0 {
			log.Printf("%s %s %d %dB %s: %s", r.Method, r.URL.Path, rec.status, rec.bytes, time.Since(start).Round(time.Millisecond), strings.TrimSpace(string(rec.errMsg)))
			return
		}
		log.Printf("%s %s %d %dB %s", r.Method, r.URL.Path, rec.status, rec.bytes, time.Since(start).Round(time.Millisecond))
	})
}
