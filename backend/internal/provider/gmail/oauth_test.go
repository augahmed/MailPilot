package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/credentials"
)

func TestAuthorizationUsesPKCEOfflineAndOnlyModifyScope(t *testing.T) {
	g := NewOAuth("synthetic-client", credentials.NewSecret("synthetic-secret"))
	u, err := url.Parse(g.AuthorizationURL("http://127.0.0.1:1234/oauth/callback", "state", "verifier"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("scope") != ModifyScope || q.Get("state") != "state" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != oauth2.S256ChallengeFromVerifier("verifier") || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("response_type") != "code" {
		t.Fatal("incorrect authorization parameters")
	}
	if strings.Contains(u.String(), "synthetic-secret") || q.Get("code_verifier") != "" {
		t.Fatal("secret in browser URL")
	}
}
func TestCodeExchangeRefreshProfileAndRevocation(t *testing.T) {
	tokenCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			tokenCalls++
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "synthetic-secret" || r.Form.Get("client_id") != "synthetic-client" {
				t.Error("missing desktop client credentials")
			}
			if tokenCalls == 1 {
				if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("code") != "code" || r.Form.Get("redirect_uri") != "http://127.0.0.1:1234/oauth/callback" {
					t.Error("missing PKCE code exchange fields")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access", "refresh_token": "synthetic-refresh", "expires_in": 3600, "token_type": "Bearer", "scope": ModifyScope})
			} else {
				if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "synthetic-refresh" {
					t.Error("invalid refresh")
				}
				_, _ = fmt.Fprint(w, `{"access_token":"renewed-access","expires_in":3600,"token_type":"Bearer"}`)
			}
		case "/profile":
			if r.Header.Get("Authorization") != "Bearer renewed-access" {
				t.Error("invalid authorization")
			}
			_, _ = fmt.Fprint(w, `{"emailAddress":"Reader@example.test","messagesTotal":15}`)
		case "/revoke":
			_ = r.ParseForm()
			if r.Method != "POST" || r.Form.Get("token") != "synthetic-refresh" || r.URL.RawQuery != "" {
				t.Error("unsafe revocation")
			}
			w.WriteHeader(200)
		default:
			t.Error("unexpected mailbox request")
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	g := NewOAuth("synthetic-client", credentials.NewSecret("synthetic-secret"))
	g.config.Endpoint.TokenURL = server.URL + "/token"
	g.profileURL = server.URL + "/profile"
	g.revokeURL = server.URL + "/revoke"
	ctx := context.Background()
	grant, err := g.Exchange(ctx, "code", "http://127.0.0.1:1234/oauth/callback", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := g.Refresh(ctx, grant.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.RefreshToken.Reveal() != grant.RefreshToken.Reveal() {
		t.Fatal("omitted refresh token lost")
	}
	email, err := g.Email(ctx, renewed.AccessToken)
	if err != nil || email != "reader@example.test" {
		t.Fatal("profile not verified")
	}
	if err = g.Revoke(ctx, renewed.RefreshToken); err != nil {
		t.Fatal(err)
	}
}
func TestOAuthErrorsAreRedactedAndClassified(t *testing.T) {
	for _, code := range []string{"invalid_grant", "access_denied", "invalid_client"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "CANARY-ACCESS-REFRESH-TOKEN"})
			}))
			defer server.Close()
			g := NewOAuth("client", credentials.NewSecret("secret"))
			g.config.Endpoint.TokenURL = server.URL
			_, err := g.Refresh(context.Background(), credentials.NewSecret("synthetic-refresh"))
			if err == nil || strings.Contains(fmt.Sprintf("%+v", err), "CANARY") {
				t.Fatal("unsafe OAuth error")
			}
			if code == "invalid_grant" && !errors.Is(err, auth.ErrExpired) {
				t.Fatal("expired authorization not classified")
			}
			if code == "access_denied" && !errors.Is(err, auth.ErrDenied) {
				t.Fatal("denial not classified")
			}
		})
	}
}
func TestTokenValidationAndGrantedScope(t *testing.T) {
	for _, scope := range []string{"", "https://www.googleapis.com/auth/gmail.labels", "https://www.googleapis.com/auth/gmail.readonly"} {
		token := (&oauth2.Token{AccessToken: "synthetic", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}).WithExtra(map[string]any{"scope": scope})
		if _, err := normalized(token, true); !errors.Is(err, auth.ErrScope) {
			t.Fatal("insufficient scope accepted")
		}
	}
	if _, err := normalized(&oauth2.Token{}, false); err == nil {
		t.Fatal("empty token accepted")
	}
}
func TestNoCrossOriginTokenRedirect(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	g := NewOAuth("client", credentials.NewSecret("secret"))
	g.config.Endpoint.TokenURL = source.URL
	if _, err := g.Refresh(context.Background(), credentials.NewSecret("synthetic")); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("secret followed redirect")
	}
}

func TestContextErrorsPreservedAcrossOAuthOperations(t *testing.T) {
	g := NewOAuth("synthetic-client", credentials.NewSecret("synthetic-secret"))
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			if cause == context.Canceled {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
			}
			token := credentials.NewSecret("synthetic-token")
			operations := map[string]func() error{
				"exchange": func() error {
					_, err := g.Exchange(ctx, "synthetic-code", "http://127.0.0.1/callback", "verifier")
					return err
				},
				"refresh": func() error { _, err := g.Refresh(ctx, token); return err },
				"profile": func() error { _, err := g.Email(ctx, token); return err },
				"revoke":  func() error { return g.Revoke(ctx, token) },
			}
			for name, operation := range operations {
				if err := operation(); !errors.Is(err, cause) {
					t.Errorf("%s lost context error: %v", name, err)
				}
			}
		})
	}
}
