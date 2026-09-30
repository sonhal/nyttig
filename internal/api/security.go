package api

import (
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ── Origin ────────────────────────────────────────────────────

// ParseOrigin validates an --origin value such as "https://nyttig.example.com"
// and returns it in canonical form (scheme://host[:port], lower-case, no
// trailing slash) together with the host[:port] the Host header must match.
func ParseOrigin(raw string) (origin, host string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("origin is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse origin: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("origin must be an http or https URL, got %q", raw)
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", "", fmt.Errorf("origin %q has no host", raw)
	}
	if u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("origin must be scheme://host[:port] only, got %q", raw)
	}
	host = strings.ToLower(u.Host)
	// A default port is left out of both Origin and Host by browsers.
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return u.Scheme + "://" + host, host, nil
}

// ── Listen address guard ──────────────────────────────────────

// CheckListenAddr refuses a listen address that is not on a loopback
// interface unless allowPublic is set. nyttig-api is meant to sit behind a
// reverse proxy that does authentication, so exposing it directly by a typo
// would publish the feed and management API without a password.
func CheckListenAddr(addr string, allowPublic bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}
	if allowPublic {
		return nil
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		if host == "" {
			host = "all interfaces"
		}
		return fmt.Errorf("refusing to listen on %s (%s): not a loopback address; put nyttig-api behind a reverse proxy, or pass --allow-public-listen", addr, host)
	}
	return nil
}

// ── Middleware ────────────────────────────────────────────────

// apiCSP is the policy for API responses: they are data, never documents
// that may run or load anything.
const apiCSP = "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"

// securityHeaders sets the headers every response carries.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", apiCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// hostCheck rejects requests whose Host header is not the configured
// origin's host. It stops DNS-rebinding pages from talking to the loopback
// listener under a name the attacker controls.
func hostCheck(host string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Host, host) {
			writeError(w, http.StatusMisdirectedRequest, "unexpected Host header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isSafeMethod reports whether a method cannot change state.
func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// csrfCheck guards state-changing requests. Basic-auth credentials are sent
// automatically like cookies, so a mutation must prove it comes from our
// own page: a JSON content type (which a cross-site form cannot send without
// a CORS preflight) and a same-origin Origin or Sec-Fetch-Site header.
func csrfCheck(origin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		if !sameOrigin(r, origin) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the browser says the request came from our
// origin. Both headers are forbidden request headers: page scripts cannot
// set them.
func sameOrigin(r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" && strings.EqualFold(o, origin) {
		return true
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}
