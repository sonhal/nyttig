// Package clef is the engine of nyttig-clef, an assessor that scores news
// items with Cloudflare's Clef decision models on Workers AI. See
// docs/clef-assessor-plan.md.
//
// Clef does not generate text: it reads a state and a map of typed questions
// and returns a probability for every allowed answer. This file is the HTTP
// client for that API.
package clef

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ── Wire types ──

// Question types Clef knows and nyttig-clef uses.
const (
	TypeNoul  = "noul"  // yes/no; answers the probability of yes
	TypeScore = "score" // 2-10 levels, lowest first; answers a level in 0..n-1
)

// Question is one entry of the request's questions map. Criteria is a
// NoulCriteria (noul, optional) or a []string of levels (score).
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// NoulCriteria describes what a yes and a no mean for a noul question.
type NoulCriteria struct {
	True  string `json:"true"`
	False string `json:"false"`
}

// Request is the body POSTed to the model's run endpoint.
type Request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer is the model's answer to one question. Only the fields of the
// question's type are set. Legend and Probabilities are kept raw because
// their exact shape is not pinned down by the documentation.
type Answer struct {
	Type          string          `json:"type"`
	Noul          *float64        `json:"noul,omitempty"`
	Score         *float64        `json:"score,omitempty"`
	Legend        json.RawMessage `json:"legend,omitempty"`
	Probabilities json.RawMessage `json:"probabilities,omitempty"`
	Confidence    *float64        `json:"confidence,omitempty"`
}

// Usage is the token count Cloudflare reports for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Response is the model's answer to a request.
type Response struct {
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// ── Errors ──

// RetryableError is a 429, a 5xx or a transport failure: trying again later
// may work. RetryAfter is the server's Retry-After, zero when it sent none.
type RetryableError struct {
	Status     int // HTTP status, 0 for a transport failure
	RetryAfter time.Duration
	Msg        string
}

func (e *RetryableError) Error() string {
	if e.Status == 0 {
		return "clef: " + e.Msg
	}
	return fmt.Sprintf("clef: HTTP %d: %s", e.Status, e.Msg)
}

// PermanentError is any other failure: the same request will fail again.
type PermanentError struct {
	Status int // HTTP status, 0 when the response was malformed
	Msg    string
}

func (e *PermanentError) Error() string {
	if e.Status == 0 {
		return "clef: " + e.Msg
	}
	return fmt.Sprintf("clef: HTTP %d: %s", e.Status, e.Msg)
}

// ── Client ──

const (
	defaultBaseURL = "https://api.cloudflare.com"
	maxBodyBytes   = 1 << 20
	maxMsgLen      = 300
)

// ClientConfig configures a Client.
type ClientConfig struct {
	AccountID string
	Model     string // "clef" or "clef-flash"
	Token     string // never logged or included in an error
	// BaseURL replaces https://api.cloudflare.com. For tests only; there is
	// no config key for it.
	BaseURL string
}

// Client calls one Clef model.
type Client struct {
	endpoint string
	model    string
	token    string
	http     *http.Client
}

// NewClient builds a Client for the account and model.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.AccountID == "" || cfg.Model == "" || cfg.Token == "" {
		return nil, errors.New("clef: account id, model and token are required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	endpoint := strings.TrimRight(base, "/") + "/client/v4/accounts/" +
		url.PathEscape(cfg.AccountID) + "/ai/run/@cf/cloudflare/" + url.PathEscape(cfg.Model)
	return &Client{
		endpoint: endpoint,
		model:    cfg.Model,
		token:    cfg.Token,
		http: &http.Client{
			Timeout: 30 * time.Second,
			// A redirect would resend the bearer token somewhere else.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Decide asks the model the questions about state. The state is a JSON
// string, object or array.
func (c *Client) Decide(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	body, err := json.Marshal(Request{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return nil, &PermanentError{Msg: "encode request: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &PermanentError{Msg: "build request: " + err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The error is a *url.Error: it names the URL, never the headers.
		return nil, &RetryableError{Msg: "request failed: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &RetryableError{Status: resp.StatusCode, Msg: "read response: " + err.Error()}
	}
	if len(raw) > maxBodyBytes {
		return nil, &PermanentError{Status: resp.StatusCode, Msg: "response body over 1 MiB"}
	}

	env, envErr := parseEnvelope(raw)
	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, &RetryableError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
			Msg:        statusMessage(resp.StatusCode, env),
		}
	case resp.StatusCode >= 300:
		return nil, &PermanentError{Status: resp.StatusCode, Msg: statusMessage(resp.StatusCode, env)}
	}
	if envErr != nil {
		return nil, &PermanentError{Status: resp.StatusCode, Msg: "malformed response: " + envErr.Error()}
	}
	if env.Success != nil && !*env.Success {
		msg := env.errorText()
		if msg == "" {
			msg = "success false"
		}
		return nil, &PermanentError{Status: resp.StatusCode, Msg: msg}
	}
	out := env.Response
	if len(env.Result) > 0 && string(env.Result) != "null" {
		out = Response{}
		if err := json.Unmarshal(env.Result, &out); err != nil {
			return nil, &PermanentError{Status: resp.StatusCode, Msg: "malformed result: " + err.Error()}
		}
	}
	return &out, nil
}

// envelope is the response in either shape: Workers AI's wrapper
// ({"result": ..., "success": ...}) or the bare Response.
type envelope struct {
	Response
	Result  json.RawMessage `json:"result"`
	Success *bool           `json:"success"`
	Errors  []struct {
		Code    json.Number `json:"code"`
		Message string      `json:"message"`
	} `json:"errors"`
}

func parseEnvelope(raw []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &envelope{}, err
	}
	return &env, nil
}

func (e *envelope) errorText() string {
	var parts []string
	for _, x := range e.Errors {
		if x.Message != "" {
			parts = append(parts, x.Message)
		}
	}
	return truncate(strings.Join(parts, "; "), maxMsgLen)
}

// statusMessage is the text of an HTTP failure: the API's own error messages
// when it sent them, and a hint for a rejected token.
func statusMessage(status int, env *envelope) string {
	msg := ""
	if env != nil {
		msg = env.errorText()
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		msg += " (check that the API token has the Workers AI permission and belongs to this account)"
	}
	return msg
}

// parseRetryAfter reads a Retry-After header: seconds or an HTTP date. A
// missing or unreadable one is zero.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
