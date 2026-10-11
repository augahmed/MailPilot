package gmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"mailpilot.local/core/internal/auth"
	"mailpilot.local/core/internal/credentials"
)

// Modify is Google's narrowest scope allowing both body reads and message-label
// membership changes. It also permits sending; MailPilot exposes no sending API.
const ModifyScope = "https://www.googleapis.com/auth/gmail.modify"

type OAuth struct {
	config     oauth2.Config
	client     *http.Client
	profileURL string
	revokeURL  string
}

func NewOAuth(clientID string, clientSecret credentials.Secret) *OAuth {
	return &OAuth{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret.Reveal(),
			Scopes: []string{ModifyScope},
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL:  "https://oauth2.googleapis.com/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		client: &http.Client{
			Timeout:       20 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		profileURL: "https://gmail.googleapis.com/gmail/v1/users/me/profile",
		revokeURL:  "https://oauth2.googleapis.com/revoke",
	}
}

// Never format the underlying OAuth configuration, tokens, responses or errors.
func (*OAuth) String() string     { return "[Google OAuth adapter]" }
func (*OAuth) GoString() string   { return "[Google OAuth adapter]" }
func (g *OAuth) Configured() bool { return g.config.ClientID != "" }
func (g *OAuth) AuthorizationURL(redirect, state, verifier string) string {
	cfg := g.config
	cfg.RedirectURL = redirect
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier))
}
func safeError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var response *oauth2.RetrieveError
	if errors.As(err, &response) {
		if response.ErrorCode == "invalid_grant" {
			return auth.ErrExpired
		}
		if response.ErrorCode == "access_denied" {
			return auth.ErrDenied
		}
	}
	return auth.ErrFailed
}
func normalized(token *oauth2.Token, requireScope bool) (auth.Grant, error) {
	scope, _ := token.Extra("scope").(string)
	if scope != "" || requireScope {
		found := false
		for _, s := range strings.Fields(scope) {
			if s == ModifyScope {
				found = true
			}
		}
		if !found {
			return auth.Grant{}, auth.ErrScope
		}
	}
	if token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") || token.Expiry.IsZero() || !token.Expiry.After(time.Now()) {
		return auth.Grant{}, auth.ErrFailed
	}
	return auth.Grant{AccessToken: credentials.NewSecret(token.AccessToken), RefreshToken: credentials.NewSecret(token.RefreshToken), Expiry: token.Expiry}, nil
}
func (g *OAuth) Exchange(ctx context.Context, code, redirect, verifier string) (auth.Grant, error) {
	cfg := g.config
	cfg.RedirectURL = redirect
	token, err := cfg.Exchange(context.WithValue(ctx, oauth2.HTTPClient, g.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return auth.Grant{}, safeError(err)
	}
	grant, err := normalized(token, true)
	if err == nil && grant.RefreshToken.Empty() {
		return auth.Grant{}, auth.ErrExpired
	}
	return grant, err
}
func (g *OAuth) Refresh(ctx context.Context, refresh credentials.Secret) (auth.Grant, error) {
	if refresh.Empty() {
		return auth.Grant{}, auth.ErrExpired
	}
	token, err := g.config.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, g.client), &oauth2.Token{RefreshToken: refresh.Reveal()}).Token()
	if err != nil {
		return auth.Grant{}, safeError(err)
	}
	grant, err := normalized(token, false)
	if err != nil {
		return auth.Grant{}, err
	}
	if grant.RefreshToken.Empty() {
		grant.RefreshToken = refresh
	}
	return grant, err
}
func (g *OAuth) Email(ctx context.Context, access credentials.Secret) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.profileURL, nil)
	if err != nil {
		return "", auth.ErrFailed
	}
	request.Header.Set("Authorization", "Bearer "+access.Reveal())
	response, err := g.client.Do(request)
	if err != nil {
		return "", safeError(err)
	}
	defer response.Body.Close()
	if response.StatusCode == 401 {
		return "", auth.ErrExpired
	}
	if response.StatusCode != 200 {
		return "", auth.ErrFailed
	}
	var profile struct {
		EmailAddress string `json:"emailAddress"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 16384)).Decode(&profile) != nil || profile.EmailAddress == "" {
		return "", auth.ErrFailed
	}
	return strings.ToLower(profile.EmailAddress), nil
}
func (g *OAuth) Revoke(ctx context.Context, token credentials.Secret) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.revokeURL, strings.NewReader(url.Values{"token": {token.Reveal()}}.Encode()))
	if err != nil {
		return auth.ErrFailed
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := g.client.Do(request)
	if err != nil {
		return safeError(err)
	}
	defer response.Body.Close()
	// Google returns invalid_token for tokens already revoked/expired.
	if response.StatusCode == 200 {
		return nil
	}
	if response.StatusCode == 400 {
		var data struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&data) == nil && data.Error == "invalid_token" {
			return nil
		}
	}
	return auth.ErrFailed
}
