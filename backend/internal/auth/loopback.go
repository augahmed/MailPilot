package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

type callback struct {
	code string
	err  error
}
type loopback struct {
	used     atomic.Bool
	redirect string
	state    string
	verifier string
	server   *http.Server
	result   chan callback
}

func newLoopback() (*loopback, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, ErrFailed
	}
	l := &loopback{redirect: "http://" + listener.Addr().String() + "/oauth/callback", state: rand.Text(), verifier: rand.Text() + rand.Text(), result: make(chan callback, 1)}
	l.server = &http.Server{Handler: http.HandlerFunc(l.handle), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	go func() { _ = l.server.Serve(listener) }()
	return l, nil
}
func (l *loopback) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	target, _ := url.Parse(l.redirect)
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(r.URL.RawQuery) > 8192 || r.Method != http.MethodGet || r.URL.Path != target.Path || r.Host != target.Host || len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(l.state)) != 1 {
		http.Error(w, "Invalid authorization callback.", http.StatusBadRequest)
		return
	}
	result := callback{}
	if len(q["error"]) == 1 && len(q["code"]) == 0 {
		result.err = ErrFailed
		if q.Get("error") == "access_denied" {
			result.err = ErrDenied
		}
	} else if len(q["code"]) == 1 && q.Get("code") != "" && len(q["error"]) == 0 {
		result.code = q.Get("code")
	} else {
		http.Error(w, "Invalid authorization callback.", http.StatusBadRequest)
		return
	}
	if !l.used.CompareAndSwap(false, true) {
		http.Error(w, "Authorization response already received.", http.StatusConflict)
		return
	}
	select {
	case l.result <- result:
		_, _ = io.WriteString(w, "Authorization response received. Return to MailPilot to see the result.")
	default:
		http.Error(w, "Authorization response already received.", http.StatusConflict)
	}
}
func (l *loopback) wait(ctx context.Context) (string, error) {
	defer l.close()
	select {
	case result := <-l.result:
		return result.code, result.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrTimeout
		}
		return "", ctx.Err()
	}
}
func validEmail(email string) bool {
	return len(email) <= 320 && strings.Contains(email, "@") && !strings.ContainsAny(email, "\r\n\x00 ")
}

func (l *loopback) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.server.Shutdown(ctx)
	_ = l.server.Close()
}
