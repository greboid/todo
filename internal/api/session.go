package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Browser sessions for the optional API-key guard.
//
// Non-browser clients present the API key in a header; browsers get a
// short-lived session cookie instead, minted when the SPA document is served
// and re-minted on every session-authenticated request (sliding expiration),
// so an actively used tab stays alive while an idle one lapses. Tokens are
// stateless — "<expiry-unix>.<hex HMAC>" — signed with a key derived from
// (never equal to) the API key: the key itself never appears in a cookie,
// rotating the key invalidates every session, and server restarts do not.

// sessionCookieName is the cookie browsers use in place of the API key.
const sessionCookieName = "todo_session"

// sessionTTL is how long a session lives without activity.
const sessionTTL = time.Hour

// sessionKey derives the token-signing key from the API key.
func sessionKey(apiKey string) []byte {
	sum := sha256.Sum256(append([]byte("todo-session-v1:"), apiKey...))
	return sum[:]
}

// mintSessionToken signs an expiry into a session token valid from now until
// sessionTTL has elapsed.
func mintSessionToken(key []byte, now time.Time) string {
	exp := strconv.FormatInt(now.Add(sessionTTL).Unix(), 10)
	return exp + "." + hex.EncodeToString(sessionSig(key, exp))
}

// validSessionToken reports whether token is correctly signed and unexpired
// as of now. The signature is compared in constant time.
func validSessionToken(key []byte, token string, now time.Time) bool {
	exp, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	expAt, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || !now.Before(time.Unix(expAt, 0)) {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	return hmac.Equal(want, sessionSig(key, exp))
}

func sessionSig(key []byte, exp string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = io.WriteString(mac, "exp="+exp)
	return mac.Sum(nil)
}

// MintSession sets a fresh session cookie when an API key is configured; a
// no-op otherwise. main wires it into the SPA handler so loading the UI
// document bootstraps a session, and requireAPIKey calls it again on every
// session-authenticated request for the sliding refresh. The cookie is
// marked Secure when the request arrived over TLS or through a reverse
// proxy that advertises HTTPS, so plain-HTTP LAN deployments keep working
// while TLS ones get the hardened flag.
func (h *Handler) MintSession(w http.ResponseWriter, r *http.Request) {
	if h.sessKey == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    mintSessionToken(h.sessKey, time.Now()),
		Path:     "/api",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// requestIsSecure reports whether the request reached this server over TLS,
// directly or via a proxy that forwarded the original protocol. A
// comma-separated X-Forwarded-Proto (chained proxies) takes its first entry.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// validSession reports whether the request carries an unexpired session
// cookie. Always false when no API key is configured.
func (h *Handler) validSession(r *http.Request) bool {
	if h.sessKey == nil {
		return false
	}
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return validSessionToken(h.sessKey, c.Value, time.Now())
}
