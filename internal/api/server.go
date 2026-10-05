// Package api is nyttig-api, the web client's HTTP API: JSON and Server-Sent Events over
// the daemon's gRPC API, behind security middleware. The SvelteKit app in
// web/ is a separate Node service; the reverse proxy sends /api/* here and
// everything else to it.
//
// The package only depends on the generated pb.NyttigClient interface, so
// handlers are tested against a fake and could be mounted elsewhere.
package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// Config configures the handler returned by New.
type Config struct {
	// Client talks to nyttigd. Required.
	Client pb.NyttigClient
	// Origin is the public origin the app is served from, such as
	// "https://nyttig.example.com". Required: it drives the CSRF and Host
	// checks.
	Origin string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// PingInterval between SSE keep-alive comments. Default 20s.
	PingInterval time.Duration
	// StreamQueue is the per-connection SSE queue length. Default 1024.
	StreamQueue int
	// Now is the clock that turns a saved view's window ("since:1d") into a
	// cutoff for ?view=. Default time.Now; tests fix it.
	Now func() time.Time
}

// ── Construction ──────────────────────────────────────────────

// New returns the nyttig-api handler: the API, SSE and security middleware.
func New(cfg Config) (http.Handler, error) {
	if cfg.Client == nil {
		return nil, errors.New("web: Client is required")
	}
	origin, host, err := ParseOrigin(cfg.Origin)
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = defaultPingInterval
	}
	if cfg.StreamQueue <= 0 {
		cfg.StreamQueue = defaultQueueSize
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &handlers{client: cfg.Client, now: cfg.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", a.health)
	mux.HandleFunc("GET /api/sources", a.listSources)
	mux.HandleFunc("POST /api/sources", a.addSource)
	mux.HandleFunc("PATCH /api/sources/{id}", a.updateSource)
	mux.HandleFunc("DELETE /api/sources/{id}", a.removeSource)
	mux.HandleFunc("GET /api/tags", a.listTags)
	mux.HandleFunc("POST /api/tags", a.addTag)
	mux.HandleFunc("PATCH /api/tags/{id}", a.updateTag)
	mux.HandleFunc("DELETE /api/tags/{id}", a.removeTag)
	mux.HandleFunc("GET /api/rules", a.listRules)
	mux.HandleFunc("POST /api/rules", a.addRule)
	mux.HandleFunc("DELETE /api/rules/{id}", a.removeRule)
	mux.HandleFunc("POST /api/rules/test", a.testRule)
	mux.HandleFunc("GET /api/views", a.listViews)
	mux.HandleFunc("POST /api/views", a.addView)
	mux.HandleFunc("PUT /api/views/order", a.reorderViews)
	mux.HandleFunc("PATCH /api/views/{id}", a.updateView)
	mux.HandleFunc("DELETE /api/views/{id}", a.removeView)
	mux.HandleFunc("GET /api/assessors", a.listAssessors)
	mux.HandleFunc("POST /api/assessors", a.addAssessor)
	mux.HandleFunc("PATCH /api/assessors/{id}", a.updateAssessor)
	mux.HandleFunc("DELETE /api/assessors/{id}", a.removeAssessor)
	mux.HandleFunc("GET /api/digest-series", a.listDigestSeries)
	mux.HandleFunc("POST /api/digest-series", a.addDigestSeries)
	mux.HandleFunc("PUT /api/digest-series/order", a.reorderDigestSeries)
	mux.HandleFunc("PATCH /api/digest-series/{id}", a.updateDigestSeries)
	mux.HandleFunc("DELETE /api/digest-series/{id}", a.removeDigestSeries)
	mux.HandleFunc("GET /api/digests", a.listDigests)
	mux.HandleFunc("POST /api/digests", a.addDigest)
	mux.HandleFunc("GET /api/digests/{id}", a.getDigest)
	mux.HandleFunc("PATCH /api/digests/{id}", a.updateDigest)
	mux.HandleFunc("DELETE /api/digests/{id}", a.removeDigest)
	mux.HandleFunc("PUT /api/items/{id}/assessments", a.putAssessment)
	mux.HandleFunc("DELETE /api/items/{id}/assessments", a.removeAssessment)
	mux.HandleFunc("POST /api/refresh", a.refresh)
	mux.HandleFunc("GET /api/items", a.items)
	mux.HandleFunc("POST /api/viewed", a.viewed)
	mux.Handle("GET /api/stream", &streamHandler{
		client:       cfg.Client,
		pingInterval: cfg.PingInterval,
		queueSize:    cfg.StreamQueue,
		log:          cfg.Logger,
		now:          cfg.Now,
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})

	var h http.Handler = mux
	h = csrfCheck(origin, h)
	h = hostCheck(host, h)
	h = securityHeaders(h)
	return h, nil
}
