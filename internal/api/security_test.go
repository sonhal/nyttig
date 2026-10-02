package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseOrigin(t *testing.T) {
	for _, tc := range []struct {
		in, origin, host string
		ok               bool
	}{
		{"https://nyttig.example.com", "https://nyttig.example.com", "nyttig.example.com", true},
		{"https://Nyttig.Example.com/", "https://nyttig.example.com", "nyttig.example.com", true},
		{"https://nyttig.example.com:443", "https://nyttig.example.com", "nyttig.example.com", true},
		{"http://127.0.0.1:7070", "http://127.0.0.1:7070", "127.0.0.1:7070", true},
		{"http://[::1]:80", "http://[::1]", "[::1]", true},
		{"", "", "", false},
		{"nyttig.example.com", "", "", false},
		{"ftp://nyttig.example.com", "", "", false},
		{"https://nyttig.example.com/app", "", "", false},
		{"https://user@nyttig.example.com", "", "", false},
		{"https://nyttig.example.com?x=1", "", "", false},
	} {
		origin, host, err := ParseOrigin(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseOrigin(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if origin != tc.origin || host != tc.host {
			t.Errorf("ParseOrigin(%q) = %q, %q; want %q, %q", tc.in, origin, host, tc.origin, tc.host)
		}
	}
}

func TestCheckListenAddr(t *testing.T) {
	for _, tc := range []struct {
		addr        string
		allowPublic bool
		ok          bool
	}{
		{"127.0.0.1:7070", false, true},
		{"127.1.2.3:7070", false, true},
		{"[::1]:7070", false, true},
		{"localhost:7070", false, true},
		{"0.0.0.0:7070", false, false},
		{":7070", false, false},
		{"[::]:7070", false, false},
		{"192.168.1.10:7070", false, false},
		{"nyttig.example.com:7070", false, false},
		{"0.0.0.0:7070", true, true},
		{":7070", true, true},
		{"127.0.0.1", false, false}, // no port
	} {
		err := CheckListenAddr(tc.addr, tc.allowPublic)
		if (err == nil) != tc.ok {
			t.Errorf("CheckListenAddr(%q, %v) = %v, want ok=%v", tc.addr, tc.allowPublic, err, tc.ok)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	th := newTestHandler(t, &fakeClient{})
	// Every kind of response carries them, errors included.
	for _, target := range []string{"/api/sources", "/api/nope", "/"} {
		rec := th.do("GET", target, "", nil)
		h := rec.Header()
		for k, want := range map[string]string{
			"X-Content-Type-Options":     "nosniff",
			"Referrer-Policy":            "no-referrer",
			"Cross-Origin-Opener-Policy": "same-origin",
			"X-Frame-Options":            "DENY",
		} {
			if got := h.Get(k); got != want {
				t.Errorf("%s: %s = %q, want %q", target, k, got, want)
			}
		}
		csp := h.Get("Content-Security-Policy")
		for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
			if !strings.Contains(csp, want) {
				t.Errorf("%s: CSP %q lacks %q", target, csp, want)
			}
		}
	}
}

func TestHostCheck(t *testing.T) {
	th := newTestHandler(t, &fakeClient{})
	for host, want := range map[string]int{
		"nyttig.example.com":      200,
		"NYTTIG.example.com":      200,
		"evil.example.com":        http.StatusMisdirectedRequest,
		"nyttig.example.com:8443": http.StatusMisdirectedRequest,
		"127.0.0.1:7070":          http.StatusMisdirectedRequest,
	} {
		rec := th.do("GET", "/api/health", "", map[string]string{"Host": host})
		if rec.Code != want {
			t.Errorf("Host %q: status %d, want %d", host, rec.Code, want)
		}
	}
}

func TestCSRF(t *testing.T) {
	th := newTestHandler(t, &fakeClient{})
	for _, tc := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"same origin", nil, http.StatusNoContent},
		{"json with charset", map[string]string{"Content-Type": "application/json; charset=utf-8"}, http.StatusNoContent},
		{"sec-fetch-site only", map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"}, http.StatusNoContent},
		{"no origin headers", map[string]string{"Origin": ""}, http.StatusForbidden},
		{"cross origin", map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross site fetch", map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same site is not same origin", map[string]string{"Origin": "", "Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"null origin", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"form post", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"text plain", map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"no content type", map[string]string{"Content-Type": ""}, http.StatusUnsupportedMediaType},
	} {
		rec := th.do("POST", "/api/viewed", `{"ids":[]}`, tc.hdr)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body)
		}
	}
	// Safe methods need neither header.
	if rec := th.do("GET", "/api/sources", "", map[string]string{"Origin": "https://evil.example"}); rec.Code != 200 {
		t.Errorf("GET with foreign Origin: %d", rec.Code)
	}
}

// The saved-views routes sit behind the same Host and CSRF checks.
func TestViewsRoutesAreGuarded(t *testing.T) {
	fc := &fakeClient{}
	th := newTestHandler(t, fc)
	for _, rq := range []struct{ method, path, body string }{
		{"POST", "/api/views", `{"name":"x"}`},
		{"PATCH", "/api/views/1", `{"name":"x"}`},
		{"DELETE", "/api/views/1", ``},
		{"PUT", "/api/views/order", `{"ids":["1"]}`},
	} {
		for name, hdr := range map[string]map[string]string{
			"cross origin": {"Origin": "https://evil.example"},
			"no origin":    {"Origin": ""},
			"form":         {"Content-Type": "text/plain"},
		} {
			if rec := th.do(rq.method, rq.path, rq.body, hdr); rec.Code != http.StatusForbidden && rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s %s (%s): %d, want 403 or 415", rq.method, rq.path, name, rec.Code)
			}
		}
	}
	if rec := th.do("GET", "/api/views", "", map[string]string{"Host": "evil.example"}); rec.Code != http.StatusForbidden && rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("GET with a foreign Host: %d", rec.Code)
	}
	if fc.lastReq != nil || len(fc.removed) != 0 {
		t.Errorf("a guarded request reached the daemon: %v %v", fc.lastReq, fc.removed)
	}
}
