// Package scheduler manages per-source fetch goroutines. Each source gets its
// own goroutine with a time.Ticker at its refresh_sec interval. On start, the
// first fetch runs immediately (no initial wait). Sources can be added, removed,
// enabled, and disabled at runtime. RefreshSource triggers an out-of-cycle fetch
// without resetting the timer.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Source represents a feed source known to the scheduler.
type Source struct {
	ID         int64
	Name       string
	URL        string
	RefreshSec int64
	Enabled    bool
}

// SourceStore is the minimal interface the scheduler needs for DB operations.
type SourceStore interface {
	// ListEnabledSources returns all sources with enabled=true.
	ListEnabledSources() ([]Source, error)
	// GetSource returns a single source by ID.
	GetSource(id int64) (Source, error)
}

// FetchFunc is called when a source should be fetched. It receives the source
// and a context that is cancelled when the source is stopped.
type FetchFunc func(ctx context.Context, source Source) error

// Clock abstracts time operations for testability.
type Clock interface {
	NewTicker(d time.Duration) Ticker
}

// Ticker abstracts time.Ticker.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// realClock wraps the standard library time package.
type realClock struct{}

func (realClock) NewTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

type realTicker struct {
	t *time.Ticker
}

func (rt *realTicker) C() <-chan time.Time { return rt.t.C }
func (rt *realTicker) Stop()               { rt.t.Stop() }

// Scheduler manages per-source fetch goroutines.
type Scheduler struct {
	store   SourceStore
	fetchFn FetchFunc
	clock   Clock
	logger  *slog.Logger

	baseCtx    context.Context
	baseCancel context.CancelFunc

	mu      sync.Mutex
	sources map[int64]*runner
	wg      sync.WaitGroup
}

type runner struct {
	source    Source
	cancel    context.CancelFunc
	refreshCh chan struct{}
}

// New creates a Scheduler. If clock is nil, the real system clock is used.
// If logger is nil, slog.Default() is used.
func New(store SourceStore, fetchFn FetchFunc, clock Clock, logger *slog.Logger) *Scheduler {
	if clock == nil {
		clock = realClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	baseCtx, baseCancel := context.WithCancel(context.Background())
	return &Scheduler{
		store:      store,
		fetchFn:    fetchFn,
		clock:      clock,
		logger:     logger,
		baseCtx:    baseCtx,
		baseCancel: baseCancel,
		sources:    make(map[int64]*runner),
	}
}

// SetFetchFn replaces the fetch function. Useful when the fetch function
// needs to close over dependencies that aren't available at New() time.
func (s *Scheduler) SetFetchFn(fn FetchFunc) {
	s.fetchFn = fn
}

// Start loads all enabled sources from the store and starts a runner for each.
// Each runner triggers an immediate first fetch, then schedules at the source's
// refresh_sec interval.
func (s *Scheduler) Start(ctx context.Context) error {
	sources, err := s.store.ListEnabledSources()
	if err != nil {
		return fmt.Errorf("list enabled sources: %w", err)
	}

	for _, src := range sources {
		s.startRunner(src)
	}

	return nil
}

// AddSource starts managing a new source with an immediate first fetch. If the
// source is already running, this is a no-op.
func (s *Scheduler) AddSource(ctx context.Context, src Source) {
	s.startRunner(src)
}

// RemoveSource stops a running source and removes it from the scheduler.
func (s *Scheduler) RemoveSource(id int64) {
	s.stopRunner(id)
}

// EnableSource starts managing a source by loading it from the store and calling
// startRunner. If the source is already running, this is a no-op.
func (s *Scheduler) EnableSource(ctx context.Context, id int64) error {
	s.mu.Lock()
	_, exists := s.sources[id]
	s.mu.Unlock()
	if exists {
		return nil // already running
	}

	src, err := s.store.GetSource(id)
	if err != nil {
		return fmt.Errorf("get source %d: %w", id, err)
	}

	s.startRunner(src)
	return nil
}

// DisableSource stops a running source without removing it from the store. The
// source can be re-enabled later.
func (s *Scheduler) DisableSource(id int64) {
	s.stopRunner(id)
}

// RefreshSource triggers an immediate out-of-cycle fetch for the given source.
// The regular ticker interval is not reset. Returns an error if the source is
// not currently running.
func (s *Scheduler) RefreshSource(id int64) error {
	s.mu.Lock()
	r, ok := s.sources[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("source %d is not running", id)
	}
	select {
	case r.refreshCh <- struct{}{}:
	default:
		// A refresh is already pending; no need to enqueue another.
	}
	return nil
}

// Stop cancels all running source goroutines and waits for them to exit.
func (s *Scheduler) Stop() {
	s.baseCancel()
	s.wg.Wait()
}

// RunningSources returns the IDs of currently running sources.
func (s *Scheduler) RunningSources() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]int64, 0, len(s.sources))
	for id := range s.sources {
		ids = append(ids, id)
	}
	return ids
}

// startRunner creates a new goroutine for the given source. If the source is
// already running, this is a no-op.
func (s *Scheduler) startRunner(src Source) {
	s.mu.Lock()
	if _, exists := s.sources[src.ID]; exists {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	refreshCh := make(chan struct{}, 1)
	s.sources[src.ID] = &runner{
		source:    src,
		cancel:    cancel,
		refreshCh: refreshCh,
	}
	s.mu.Unlock()

	s.wg.Add(1)
	go s.runSource(ctx, src, refreshCh)
}

// stopRunner cancels the runner context for the given source and removes it
// from the sources map.
func (s *Scheduler) stopRunner(id int64) {
	s.mu.Lock()
	r, ok := s.sources[id]
	if ok {
		delete(s.sources, id)
	}
	s.mu.Unlock()
	if ok {
		r.cancel()
	}
}

// runSource is the per-source goroutine that waits on the ticker and refresh
// channels, calling fetchFn when triggered.
func (s *Scheduler) runSource(ctx context.Context, src Source, refreshCh chan struct{}) {
	defer s.wg.Done()

	logger := s.logger.With("source_id", src.ID, "source_name", src.Name)
	logger.Info("source scheduler started")

	// Immediate first fetch.
	s.doFetch(ctx, src)

	interval := time.Duration(src.RefreshSec) * time.Second
	if interval <= 0 {
		interval = 3600 * time.Second
	}
	ticker := s.clock.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("source scheduler stopped")
			return
		case <-ticker.C():
			s.doFetch(ctx, src)
		case <-refreshCh:
			logger.Info("out-of-cycle refresh")
			s.doFetch(ctx, src)
			// Do not reset the ticker; the regular interval continues.
		}
	}
}

// doFetch calls fetchFn with the source and logs the result.
func (s *Scheduler) doFetch(ctx context.Context, src Source) {
	logger := s.logger.With("source_id", src.ID, "source_name", src.Name)
	logger.Info("fetching source")

	if err := s.fetchFn(ctx, src); err != nil {
		logger.Warn("fetch failed", "error", err)
	}
}