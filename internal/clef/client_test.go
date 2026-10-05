package clef

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const bareBody = `{"answers":{"item":{"type":"noul","noul":0.83},
 "tag.1":{"type":"score","score":3.2,"legend":"High","probabilities":[0,0.1,0.2,0.6,0.1],"confidence":0.71}},
 "usage":{"input_tokens":120,"output_tokens":4}}`

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(ClientConfig{AccountID: "abc123", Model: "clef-flash", Token: "s3cret-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var testQuestions = map[string]Question{
	"item":  {Type: TypeNoul, Instructions: "Relevant?", Criteria: NoulCriteria{True: "yes", False: "no"}},
	"tag.1": {Type: TypeScore, Instructions: "Severity?", Criteria: []string{"None", "Low", "Medium", "High", "Critical"}},
}

func TestDecide_RequestShape(t *testing.T) {
	var gotPath, gotAuth, gotCT, gotMethod string
	var gotBody map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotCT, gotMethod = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(bareBody))
	})
	state := map[string]any{"title": "T", "text": "body"}
	if _, err := c.Decide(context.Background(), state, testQuestions); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if want := "/client/v4/accounts/abc123/ai/run/@cf/cloudflare/clef-flash"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotAuth != "Bearer s3cret-token" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotBody["model"] != "clef-flash" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if s, _ := gotBody["state"].(map[string]any); s["title"] != "T" {
		t.Errorf("state = %v", gotBody["state"])
	}
	qs, _ := gotBody["questions"].(map[string]any)
	item, _ := qs["item"].(map[string]any)
	if item["type"] != "noul" || item["instructions"] != "Relevant?" {
		t.Errorf("item question = %v", item)
	}
	if crit, _ := item["criteria"].(map[string]any); crit["true"] != "yes" || crit["false"] != "no" {
		t.Errorf("noul criteria = %v", item["criteria"])
	}
	tag, _ := qs["tag.1"].(map[string]any)
	if crit, _ := tag["criteria"].([]any); len(crit) != 5 || crit[3] != "High" {
		t.Errorf("score criteria = %v", tag["criteria"])
	}
}

func TestDecide_ResponseShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"bare", bareBody},
		{"wrapped", `{"result":` + bareBody + `,"success":true,"errors":[],"messages":[]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.body)) })
			resp, err := c.Decide(context.Background(), "x", testQuestions)
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.Answers["item"]; got.Type != "noul" || got.Noul == nil || *got.Noul != 0.83 {
				t.Errorf("item answer = %+v", got)
			}
			if got := resp.Answers["tag.1"]; got.Score == nil || *got.Score != 3.2 || got.Confidence == nil || *got.Confidence != 0.71 {
				t.Errorf("tag answer = %+v", got)
			}
			if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 4 {
				t.Errorf("usage = %+v", resp.Usage)
			}
		})
	}
}

func TestDecide_Errors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		header     map[string]string
		body       string
		retryable  bool
		retryAfter time.Duration
		wantMsg    string
	}{
		{"envelope success false", 200, nil, `{"success":false,"errors":[{"code":1001,"message":"bad question"}],"result":null}`, false, 0, "bad question"},
		{"api error envelope 400", 400, nil, `{"success":false,"errors":[{"code":5006,"message":"invalid input"}]}`, false, 0, "invalid input"},
		{"401 hint", 401, nil, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`, false, 0, "Workers AI permission"},
		{"403 hint", 403, nil, ``, false, 0, "Workers AI permission"},
		{"429 without retry-after", 429, nil, ``, true, 0, "HTTP 429"},
		{"429 with retry-after", 429, map[string]string{"Retry-After": "7"}, `{}`, true, 7 * time.Second, "HTTP 429"},
		{"500", 500, nil, `oops`, true, 0, "HTTP 500"},
		{"503 envelope", 503, nil, `{"errors":[{"code":1,"message":"overloaded"}]}`, true, 0, "overloaded"},
		{"malformed json", 200, nil, `{"answers":`, false, 0, "malformed response"},
		{"malformed result", 200, nil, `{"result":"nope"}`, false, 0, "malformed result"},
		{"redirect refused", 302, map[string]string{"Location": "http://example.invalid/"}, ``, false, 0, "HTTP 302"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := c.Decide(context.Background(), "x", testQuestions)
			if err == nil {
				t.Fatal("want an error")
			}
			var re *RetryableError
			isRetry := errors.As(err, &re)
			if isRetry != tc.retryable {
				t.Fatalf("retryable = %v, want %v (%v)", isRetry, tc.retryable, err)
			}
			if isRetry && re.RetryAfter != tc.retryAfter {
				t.Errorf("RetryAfter = %v, want %v", re.RetryAfter, tc.retryAfter)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err, tc.wantMsg)
			}
			if strings.Contains(err.Error(), "s3cret-token") {
				t.Errorf("error leaks the token: %v", err)
			}
		})
	}
}

func TestDecide_RedirectNotFollowed(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		_, _ = w.Write([]byte(bareBody))
	}))
	defer other.Close()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	})
	if _, err := c.Decide(context.Background(), "x", testQuestions); err == nil {
		t.Fatal("want an error")
	}
	if hit {
		t.Error("the redirect was followed")
	}
}

func TestDecide_OversizedBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{},"pad":"` + strings.Repeat("a", maxBodyBytes) + `"}`))
	})
	_, err := c.Decide(context.Background(), "x", testQuestions)
	var pe *PermanentError
	if !errors.As(err, &pe) || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("err = %v, want a permanent over-1-MiB error", err)
	}
}

func TestDecide_TransportErrorIsRetryableAndHidesToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // connection refused
	c, err := NewClient(ClientConfig{AccountID: "abc", Model: "clef", Token: "s3cret-token", BaseURL: base})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Decide(context.Background(), "x", testQuestions)
	var re *RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("err = %v, want retryable", err)
	}
	if strings.Contains(err.Error(), "s3cret-token") {
		t.Errorf("error leaks the token: %v", err)
	}
}

func TestDecide_ContextCancelled(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(bareBody)) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Decide(ctx, "x", testQuestions); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestNewClient_Validation(t *testing.T) {
	for _, cfg := range []ClientConfig{
		{Model: "clef", Token: "t"},
		{AccountID: "a", Token: "t"},
		{AccountID: "a", Model: "clef"},
	} {
		if _, err := NewClient(cfg); err == nil {
			t.Errorf("NewClient(%+v) = nil error", cfg)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"30", 30 * time.Second},
		{"-5", 0},
		{"garbage", 0},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	}
	for _, tc := range tests {
		if got := parseRetryAfter(tc.in, now); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
