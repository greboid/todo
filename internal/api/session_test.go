package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSessionToken(t *testing.T) {
	key := sessionKey("secret")
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	token := mintSessionToken(key, now)
	exp, sig, _ := strings.Cut(token, ".")

	flipLast := func(s string) string {
		last := s[len(s)-1]
		if last == '0' {
			return s[:len(s)-1] + "1"
		}
		return s[:len(s)-1] + "0"
	}

	tests := []struct {
		name  string
		token string
		at    time.Time
		want  bool
	}{
		{"fresh", token, now, true},
		{"just before expiry", token, now.Add(sessionTTL - time.Second), true},
		{"at expiry", token, now.Add(sessionTTL), false},
		{"long after expiry", token, now.Add(24 * time.Hour), false},
		{"tampered expiry", exp + "0." + sig, now, false},
		{"tampered signature", exp + "." + flipLast(sig), now, false},
		{"missing signature", exp, now, false},
		{"garbage", "hello", now, false},
		{"empty", "", now, false},
	}
	for _, tt := range tests {
		if got := validSessionToken(key, tt.token, tt.at); got != tt.want {
			t.Errorf("%s: validSessionToken(%q) = %v, want %v", tt.name, tt.token, got, tt.want)
		}
	}
	if validSessionToken(sessionKey("other"), token, now) {
		t.Error("token minted under another API key must not validate")
	}
}

func TestRequireAPIKeySession(t *testing.T) {
	h := New(nil, "secret")
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	guarded := h.requireAPIKey(ok)

	cookieValue := func(rec *httptest.ResponseRecorder) string {
		v := rec.Header().Get("Set-Cookie")
		if v == "" {
			t.Fatal("no Set-Cookie in response")
		}
		return strings.TrimPrefix(strings.Split(v, ";")[0], sessionCookieName+"=")
	}

	get := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
		if cookie != "" {
			req.Header.Set("Cookie", sessionCookieName+"="+cookie)
		}
		rec := httptest.NewRecorder()
		guarded.ServeHTTP(rec, req)
		return rec
	}

	minted := httptest.NewRecorder()
	h.MintSession(minted, httptest.NewRequest(http.MethodGet, "/", nil))
	mintedCookie := cookieValue(minted)

	tests := []struct {
		name   string
		cookie string
		want   int
	}{
		{"no cookie", "", http.StatusUnauthorized},
		{"minted cookie", mintedCookie, http.StatusOK},
		{"expired cookie", mintSessionToken(h.sessKey, time.Now().Add(-2*sessionTTL)), http.StatusUnauthorized},
		{"forged cookie", mintedCookie + "x", http.StatusUnauthorized},
	}
	for _, tt := range tests {
		if rec := get(tt.cookie); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
	}

	rec := get(mintedCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("session request: status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") == "" {
		t.Error("session-authenticated response must re-mint the cookie (sliding refresh)")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	guarded.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("raw API key: status = %d, want 200", rec.Code)
	}

	open := New(nil, "").requireAPIKey(ok)
	rec = httptest.NewRecorder()
	open.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/todos", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("no key configured: status = %d, want 200", rec.Code)
	}
}

func TestMintSessionSecure(t *testing.T) {
	h := New(nil, "secret")
	mint := func(req *http.Request) string {
		rec := httptest.NewRecorder()
		h.MintSession(rec, req)
		return rec.Header().Get("Set-Cookie")
	}

	tests := []struct {
		name string
		req  *http.Request
		want bool
	}{
		{"plain http", httptest.NewRequest(http.MethodGet, "/", nil), false},
		{
			"proxied https",
			withHeader(httptest.NewRequest(http.MethodGet, "/", nil), "X-Forwarded-Proto", "https"),
			true,
		},
		{
			"chained proxy list",
			withHeader(httptest.NewRequest(http.MethodGet, "/", nil), "X-Forwarded-Proto", "https, http"),
			true,
		},
		{
			"proxied http",
			withHeader(httptest.NewRequest(http.MethodGet, "/", nil), "X-Forwarded-Proto", "http"),
			false,
		},
		{"direct tls", withTLS(httptest.NewRequest(http.MethodGet, "/", nil)), true},
	}
	for _, tt := range tests {
		if got := strings.Contains(mint(tt.req), "; Secure"); got != tt.want {
			t.Errorf("%s: cookie Secure = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func withHeader(req *http.Request, key, value string) *http.Request {
	req.Header.Set(key, value)
	return req
}

func withTLS(req *http.Request) *http.Request {
	req.TLS = &tls.ConnectionState{}
	return req
}
