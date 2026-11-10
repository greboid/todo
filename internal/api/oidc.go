package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Optional OpenID Connect authentication.
//
// Browsers log in with the authorization code flow (with PKCE): a page load
// without a valid session redirects to the provider, and the provider sends
// the browser back to /auth/callback, which verifies the ID token and mints
// the same stateless session cookie the API-key mode uses (see session.go),
// signed with a key derived from the configured session key. API clients
// instead send a provider-issued JWT access token as a Bearer token, checked
// against the issuer's published keys, its expiry, and the audience.

// OIDCConfig holds the settings for OpenID Connect authentication.
type OIDCConfig struct {
	// Issuer is the provider's issuer URL, used for discovery.
	Issuer string
	// ClientID and ClientSecret identify this instance to the provider for
	// the browser login flow.
	ClientID     string
	ClientSecret string
	// SessionKey signs browser session cookies and login state. Keeping it
	// stable keeps sessions valid across restarts; changing it ends them.
	SessionKey string
	// Audience is the value access tokens must carry in their aud claim.
	Audience string
}

// OIDC verifies browser logins and bearer access tokens against one
// provider. Create it with NewOIDC and attach it with Handler.WithOIDC.
type OIDC struct {
	oauth      oauth2.Config
	idVerifier *oidc.IDTokenVerifier
	atVerifier *oidc.IDTokenVerifier
	audience   string
	sessKey    []byte
	flowKey    []byte
}

// NewOIDC discovers the provider's endpoints and keys from the issuer. The
// provider must be reachable: discovery happens once, here.
func NewOIDC(ctx context.Context, cfg OIDCConfig) (*OIDC, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.SessionKey == "" || cfg.Audience == "" {
		return nil, errors.New("issuer, client ID, client secret, session key and audience are all required")
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("discover provider: %w", err)
	}
	return &OIDC{
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID},
		},
		idVerifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		// The audience is checked separately, as access tokens carry this
		// instance's audience rather than the client ID.
		atVerifier: provider.Verifier(&oidc.Config{SkipClientIDCheck: true}),
		audience:   strings.TrimSuffix(cfg.Audience, "/"),
		sessKey:    derivedKey("todo-oidc-session-v1:", cfg.SessionKey),
		flowKey:    derivedKey("todo-oidc-flow-v1:", cfg.SessionKey),
	}, nil
}

// WithOIDC turns on OpenID Connect authentication. Browser sessions are then
// only minted by a completed login, never by simply loading the page, and the
// session cookie covers the whole site so page loads can check it. An API key
// configured alongside keeps working for API clients.
func (h *Handler) WithOIDC(o *OIDC) *Handler {
	h.oidc = o
	h.sessKey = o.sessKey
	return h
}

// OIDCEnabled reports whether OpenID Connect authentication is configured.
func (h *Handler) OIDCEnabled() bool {
	return h.oidc != nil
}

// AuthorizePage guards a page load (the SPA document or the docs page). It
// reports whether the page should be served; when it returns false it has
// already written the response. With OIDC, a request without a valid session
// is redirected to the provider to log in. Otherwise it mints a session
// (when an API key is configured) and lets the page through.
func (h *Handler) AuthorizePage(w http.ResponseWriter, r *http.Request) bool {
	if h.oidc == nil {
		h.MintSession(w, r)
		return true
	}
	if h.validSession(r) {
		h.MintSession(w, r)
		return true
	}
	h.startLogin(w, r)
	return false
}

// callbackPath is where the provider returns the browser after login. It has
// to be registered with the provider as a redirect URI, as
// <scheme>://<host>/auth/callback.
const callbackPath = "/auth/callback"

// flowCookiePrefix names the short-lived cookie that carries one login
// attempt's state, PKCE verifier and nonce across the provider round trip.
// The state is part of the name, so several tabs logging in at once (all of
// them reloading after their sessions lapsed) don't overwrite each other.
const flowCookiePrefix = "todo_oidc_"

// flowTTL bounds how long a login may spend at the provider.
const flowTTL = 10 * time.Minute

// loginFlow is the state of one login attempt, carried in a signed cookie.
type loginFlow struct {
	Verifier string `json:"v"`
	Nonce    string `json:"n"`
	Return   string `json:"r"`
	Expires  int64  `json:"e"`
}

// startLogin redirects the browser to the provider's authorization endpoint,
// remembering where to send it once it comes back.
func (h *Handler) startLogin(w http.ResponseWriter, r *http.Request) {
	state := rand.Text()
	flow := loginFlow{
		Verifier: oauth2.GenerateVerifier(),
		Nonce:    rand.Text(),
		Return:   r.URL.RequestURI(),
		Expires:  time.Now().Add(flowTTL).Unix(),
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookiePrefix + state,
		Value:    h.signFlow(flow),
		Path:     callbackPath,
		MaxAge:   int(flowTTL / time.Second),
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		// Lax, not Strict: the provider's redirect back is a cross-site
		// top-level navigation, and the cookie has to ride along with it.
		SameSite: http.SameSiteLaxMode,
	})
	cfg := h.oidc.oauth
	cfg.RedirectURL = requestOrigin(r) + callbackPath
	target := cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(flow.Verifier), oidc.Nonce(flow.Nonce))
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

// Callback completes a browser login: it checks the state against the flow
// cookie, exchanges the code (with the PKCE verifier), verifies the ID token
// and its nonce, mints a session and sends the browser back to the page it
// started from. Failures end in an error response rather than another
// redirect, so a misconfiguration can't loop.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		http.Error(w, "login failed: missing state", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(flowCookiePrefix + state)
	if err != nil {
		http.Error(w, "login failed: unknown or expired login attempt; reload the page to try again", http.StatusBadRequest)
		return
	}
	// One use only, whatever happens next.
	http.SetCookie(w, &http.Cookie{
		Name:     cookie.Name,
		Path:     callbackPath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	flow, ok := h.verifyFlow(cookie.Value, time.Now())
	if !ok {
		http.Error(w, "login failed: unknown or expired login attempt; reload the page to try again", http.StatusBadRequest)
		return
	}
	if e := q.Get("error"); e != "" {
		slog.Warn("oidc login refused by provider", "error", e, "description", q.Get("error_description"))
		http.Error(w, "login failed: the provider refused the login ("+e+")", http.StatusUnauthorized)
		return
	}

	cfg := h.oidc.oauth
	cfg.RedirectURL = requestOrigin(r) + callbackPath
	tok, err := cfg.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		slog.Warn("oidc code exchange failed", "error", err)
		http.Error(w, "login failed: could not exchange the authorization code", http.StatusBadGateway)
		return
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		http.Error(w, "login failed: the provider returned no ID token", http.StatusBadGateway)
		return
	}
	idToken, err := h.oidc.idVerifier.Verify(r.Context(), rawID)
	if err != nil {
		slog.Warn("oidc id token rejected", "error", err)
		http.Error(w, "login failed: invalid ID token", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		http.Error(w, "login failed: ID token nonce mismatch", http.StatusUnauthorized)
		return
	}

	h.MintSession(w, r)
	http.Redirect(w, r, safeReturn(flow.Return), http.StatusFound)
}

// validAccessToken reports whether token is a JWT access token from the
// configured issuer: correctly signed by one of its published keys,
// unexpired, and carrying the expected audience.
func (h *Handler) validAccessToken(r *http.Request, token string) bool {
	if h.oidc == nil {
		return false
	}
	at, err := h.oidc.atVerifier.Verify(r.Context(), token)
	if err != nil {
		slog.Debug("bearer token rejected", "error", err)
		return false
	}
	ok := slices.ContainsFunc(at.Audience, func(aud string) bool {
		return strings.TrimSuffix(aud, "/") == h.oidc.audience
	})
	if !ok {
		slog.Debug("bearer token rejected", "error", "audience mismatch", "audience", at.Audience, "want", h.oidc.audience)
	}
	return ok
}

// requestOrigin is <scheme>://<host> of the request as the browser or client
// sent it, honouring a reverse proxy's X-Forwarded-Proto.
func requestOrigin(r *http.Request) string {
	if requestIsSecure(r) {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// safeReturn keeps the post-login redirect on this site: only a local
// absolute path is allowed, never a scheme-relative or backslash form a
// browser would treat as another host.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

func (h *Handler) signFlow(f loginFlow) string {
	body, _ := json.Marshal(f)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + base64.RawURLEncoding.EncodeToString(flowSig(h.oidc.flowKey, payload))
}

func (h *Handler) verifyFlow(value string, now time.Time) (loginFlow, bool) {
	var f loginFlow
	payload, sig, ok := strings.Cut(value, ".")
	if !ok {
		return f, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, flowSig(h.oidc.flowKey, payload)) {
		return f, false
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || json.Unmarshal(body, &f) != nil {
		return f, false
	}
	if !now.Before(time.Unix(f.Expires, 0)) {
		return f, false
	}
	return f, true
}

func flowSig(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func derivedKey(purpose, secret string) []byte {
	sum := sha256.Sum256([]byte(purpose + secret))
	return sum[:]
}
