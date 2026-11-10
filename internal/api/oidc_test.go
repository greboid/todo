package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc/oidctest"
)

const (
	testClientID     = "todo-client"
	testClientSecret = "todo-secret"
	testAudience     = "https://todo.example"
)

// fakeProvider is an OpenID Connect provider for tests: discovery and keys
// come from oidctest, and its token endpoint redeems codes the test hands
// out with authorize.
type fakeProvider struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	issuer string

	mu    sync.Mutex
	codes map[string]pendingCode
	// idTokenClaims, when set, overrides the claims of the next ID token.
	idTokenClaims func(nonce string) map[string]any
}

type pendingCode struct {
	challenge   string
	nonce       string
	redirectURI string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{t: t, key: key, codes: map[string]pendingCode{}}
	base := &oidctest.Server{
		PublicKeys: []oidctest.PublicKey{{PublicKey: key.Public(), KeyID: "test-key", Algorithm: "RS256"}},
	}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			p.token(w, r)
			return
		}
		base.ServeHTTP(w, r)
	}))
	t.Cleanup(p.srv.Close)
	p.issuer = p.srv.URL
	base.SetIssuer(p.issuer)
	return p
}

// sign returns a JWT signed by the provider's key with the given claims.
func (p *fakeProvider) sign(claims map[string]any) string {
	body, err := json.Marshal(claims)
	if err != nil {
		p.t.Fatal(err)
	}
	return oidctest.SignIDToken(p.key, "test-key", "RS256", string(body))
}

// accessToken returns an access token for aud, valid for an hour.
func (p *fakeProvider) accessToken(aud string) string {
	return p.sign(map[string]any{
		"iss": p.issuer,
		"sub": "todoral",
		"aud": aud,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	})
}

// authorize plays the user logging in at the provider: given the redirect to
// the authorization endpoint, it returns the callback URL the provider would
// send the browser back to.
func (p *fakeProvider) authorize(location string) string {
	p.t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		p.t.Fatal(err)
	}
	q := u.Query()
	if got := u.Scheme + "://" + u.Host + u.Path; got != p.issuer+"/auth" {
		p.t.Fatalf("login redirect went to %s, want the provider's /auth", got)
	}
	for key, want := range map[string]string{
		"client_id":             testClientID,
		"response_type":         "code",
		"code_challenge_method": "S256",
	} {
		if q.Get(key) != want {
			p.t.Errorf("authorize %s = %q, want %q", key, q.Get(key), want)
		}
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		p.t.Errorf("authorize scope %q lacks openid", q.Get("scope"))
	}
	code := rand.Text()
	p.mu.Lock()
	p.codes[code] = pendingCode{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirectURI: q.Get("redirect_uri")}
	p.mu.Unlock()
	return q.Get("redirect_uri") + "?" + url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
}

func (p *fakeProvider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != testClientID || secret != testClientSecret {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	pending, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	claimsFn := p.idTokenClaims
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != pending.challenge || r.PostForm.Get("redirect_uri") != pending.redirectURI {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	claims := map[string]any{
		"iss":   p.issuer,
		"sub":   "chris",
		"aud":   testClientID,
		"nonce": pending.nonce,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Unix(),
	}
	if claimsFn != nil {
		claims = claimsFn(pending.nonce)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "opaque",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     p.sign(claims),
	})
}

func newOIDCHandler(t *testing.T, p *fakeProvider, apiKey string) *Handler {
	t.Helper()
	auth, err := NewOIDC(context.Background(), OIDCConfig{
		Issuer:       p.issuer,
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
		SessionKey:   "session-key",
		Audience:     testAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(nil, apiKey).WithOIDC(auth)
}

// testSite is the parts of main's routing that matter for auth: a page
// behind AuthorizePage, the callback, and a guarded API route.
func testSite(h *Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", h.Callback)
	mux.Handle("/api/", h.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if h.AuthorizePage(w, r) {
			_, _ = w.Write([]byte("page"))
		}
	})
	return mux
}

// browser is a minimal cookie-keeping client that doesn't follow redirects,
// driving the site handler in-process.
type browser struct {
	t       *testing.T
	site    http.Handler
	cookies map[string]*http.Cookie
}

func (b *browser) get(target string, header ...string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	for _, c := range b.cookies {
		if strings.HasPrefix(req.URL.Path, c.Path) {
			req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
	rec := httptest.NewRecorder()
	b.site.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c
		}
	}
	return rec
}

func TestOIDCBrowserLogin(t *testing.T) {
	p := newFakeProvider(t)
	h := newOIDCHandler(t, p, "")
	b := &browser{t: t, site: testSite(h), cookies: map[string]*http.Cookie{}}

	if rec := b.get("/api/todos"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("API without credentials: status = %d, want 401", rec.Code)
	}

	rec := b.get("/boards/2?x=1")
	if rec.Code != http.StatusFound {
		t.Fatalf("page without session: status = %d, want 302", rec.Code)
	}
	location := rec.Header().Get("Location")
	if got := mustQuery(t, location).Get("redirect_uri"); got != "http://example.com/auth/callback" {
		t.Errorf("redirect_uri = %q, want http://example.com/auth/callback", got)
	}

	rec = b.get(p.authorize(location))
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, want 302 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/boards/2?x=1" {
		t.Errorf("callback redirected to %q, want the original page", got)
	}
	session := b.cookies[sessionCookieName]
	if session == nil {
		t.Fatal("callback set no session cookie")
	}
	if session.Path != "/" {
		t.Errorf("session cookie path = %q, want /", session.Path)
	}
	for name := range b.cookies {
		if strings.HasPrefix(name, flowCookiePrefix) {
			t.Errorf("flow cookie %s left behind after the callback", name)
		}
	}

	if rec := b.get("/boards/2"); rec.Code != http.StatusOK || rec.Body.String() != "page" {
		t.Errorf("page with session: status = %d body %q, want the page", rec.Code, rec.Body.String())
	}
	if rec := b.get("/api/todos"); rec.Code != http.StatusOK {
		t.Errorf("API with session: status = %d, want 200", rec.Code)
	}
}

func TestOIDCConcurrentLogins(t *testing.T) {
	p := newFakeProvider(t)
	h := newOIDCHandler(t, p, "")
	b := &browser{t: t, site: testSite(h), cookies: map[string]*http.Cookie{}}

	// Two tabs reload at once and both head off to the provider.
	first := b.get("/one").Header().Get("Location")
	second := b.get("/two").Header().Get("Location")

	for _, tc := range []struct{ location, want string }{{second, "/two"}, {first, "/one"}} {
		rec := b.get(p.authorize(tc.location))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != tc.want {
			t.Errorf("callback for %s: status = %d location %q", tc.want, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestOIDCCallbackRejects(t *testing.T) {
	p := newFakeProvider(t)
	h := newOIDCHandler(t, p, "")

	start := func(b *browser) string {
		return p.authorize(b.get("/").Header().Get("Location"))
	}
	newBrowser := func() *browser {
		return &browser{t: t, site: testSite(h), cookies: map[string]*http.Cookie{}}
	}

	t.Run("no flow cookie", func(t *testing.T) {
		callback := start(newBrowser())
		if rec := newBrowser().get(callback); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("replayed callback", func(t *testing.T) {
		b := newBrowser()
		callback := start(b)
		b.get(callback)
		delete(b.cookies, sessionCookieName)
		if rec := b.get(callback); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("tampered flow cookie", func(t *testing.T) {
		b := newBrowser()
		callback := start(b)
		for _, c := range b.cookies {
			c.Value = "x" + c.Value
		}
		if rec := b.get(callback); rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("provider error", func(t *testing.T) {
		b := newBrowser()
		callback := start(b)
		u, _ := url.Parse(callback)
		q := u.Query()
		q.Del("code")
		q.Set("error", "access_denied")
		u.RawQuery = q.Encode()
		if rec := b.get(u.String()); rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	badIDTokens := map[string]func(nonce string) map[string]any{
		"wrong nonce": func(string) map[string]any {
			return map[string]any{"iss": p.issuer, "sub": "x", "aud": testClientID, "nonce": "other", "exp": time.Now().Add(time.Hour).Unix()}
		},
		"wrong audience": func(nonce string) map[string]any {
			return map[string]any{"iss": p.issuer, "sub": "x", "aud": "someone-else", "nonce": nonce, "exp": time.Now().Add(time.Hour).Unix()}
		},
	}
	for name, claims := range badIDTokens {
		t.Run(name, func(t *testing.T) {
			p.mu.Lock()
			p.idTokenClaims = claims
			p.mu.Unlock()
			defer func() {
				p.mu.Lock()
				p.idTokenClaims = nil
				p.mu.Unlock()
			}()
			b := newBrowser()
			if rec := b.get(start(b)); rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if b.cookies[sessionCookieName] != nil {
				t.Error("rejected login minted a session")
			}
		})
	}
}

func TestOIDCBearerTokens(t *testing.T) {
	p := newFakeProvider(t)

	tests := []struct {
		name   string
		apiKey string
		header []string
		want   int
	}{
		{"configured audience", "", []string{"Authorization", "Bearer " + p.accessToken(testAudience)}, 200},
		{"trailing slash on aud", "", []string{"Authorization", "Bearer " + p.accessToken(testAudience+"/")}, 200},
		{"wrong audience", "", []string{"Authorization", "Bearer " + p.accessToken("http://elsewhere.example")}, 401},
		// A token for another app must not pass by sending that app's host.
		{"request host is not the audience", "", []string{"Authorization", "Bearer " + p.accessToken("http://example.com")}, 401},
		{"api key not configured", "", []string{"X-API-Key", "secret"}, 401},
		{"api key as bearer alongside oidc", "secret", []string{"Authorization", "Bearer secret"}, 200},
		{"api key header alongside oidc", "secret", []string{"X-API-Key", "secret"}, 200},
		{"jwt alongside api key", "secret", []string{"Authorization", "Bearer " + p.accessToken(testAudience)}, 200},
		{"wrong bearer alongside api key", "secret", []string{"Authorization", "Bearer wrong"}, 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newOIDCHandler(t, p, tt.apiKey)
			b := &browser{t: t, site: testSite(h), cookies: map[string]*http.Cookie{}}
			if rec := b.get("/api/todos", tt.header...); rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestOIDCPageLoadNeverMintsWithoutLogin(t *testing.T) {
	p := newFakeProvider(t)
	// Even with an API key configured, which on its own mints a session for
	// any page load, OIDC requires a login first.
	h := newOIDCHandler(t, p, "secret")
	b := &browser{t: t, site: testSite(h), cookies: map[string]*http.Cookie{}}
	if rec := b.get("/"); rec.Code != http.StatusFound {
		t.Errorf("page: status = %d, want 302", rec.Code)
	}
	if b.cookies[sessionCookieName] != nil {
		t.Error("page load minted a session without a login")
	}

	routes := h.Routes()
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/swagger/", nil))
	if rec.Code != http.StatusFound {
		t.Errorf("docs page: status = %d, want 302", rec.Code)
	}
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.yaml", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("spec: status = %d, want 200", rec.Code)
	}
}

func TestOIDCStaleAPIPathCookie(t *testing.T) {
	p := newFakeProvider(t)
	h := newOIDCHandler(t, p, "")
	req := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
	// A browser sends the more specific /api cookie (left over from API-key
	// mode) first; the valid site-wide one after it must still count.
	req.Header.Set("Cookie", fmt.Sprintf("%s=stale; %s=%s", sessionCookieName, sessionCookieName, mintSessionToken(h.sessKey, time.Now())))
	rec := httptest.NewRecorder()
	testSite(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestNewOIDCRequiresSettings(t *testing.T) {
	p := newFakeProvider(t)
	full := OIDCConfig{Issuer: p.issuer, ClientID: testClientID, ClientSecret: testClientSecret, SessionKey: "k", Audience: testAudience}
	for name, mutate := range map[string]func(*OIDCConfig){
		"issuer":        func(c *OIDCConfig) { c.Issuer = "" },
		"client id":     func(c *OIDCConfig) { c.ClientID = "" },
		"client secret": func(c *OIDCConfig) { c.ClientSecret = "" },
		"session key":   func(c *OIDCConfig) { c.SessionKey = "" },
		"audience":      func(c *OIDCConfig) { c.Audience = "" },
	} {
		cfg := full
		mutate(&cfg)
		if _, err := NewOIDC(context.Background(), cfg); err == nil {
			t.Errorf("missing %s: no error", name)
		}
	}
	if _, err := NewOIDC(context.Background(), full); err != nil {
		t.Errorf("full config: %v", err)
	}
}

func TestSafeReturn(t *testing.T) {
	for in, want := range map[string]string{
		"/":                 "/",
		"/boards/1?x=y":     "/boards/1?x=y",
		"//evil.example/":   "/",
		"/\\evil.example":   "/",
		"https://evil.test": "/",
		"":                  "/",
	} {
		if got := safeReturn(in); got != want {
			t.Errorf("safeReturn(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}
