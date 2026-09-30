package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Origin: testOrigin}); err == nil {
		t.Error("New without Client succeeded")
	}
	if _, err := New(Config{Client: &fakeClient{}}); err == nil {
		t.Error("New without Origin succeeded")
	}
}

// nyttig-api serves only the API; the app is a separate service, so any
// other path is a JSON 404, never HTML.
func TestNonAPIPaths(t *testing.T) {
	th := newTestHandler(t, &fakeClient{})
	for _, target := range []string{"/", "/index.html", "/_app/immutable/x.js", "/api/nope"} {
		rec := th.do("GET", target, "", nil)
		if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}
