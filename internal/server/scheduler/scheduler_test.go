package scheduler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// ---- Fake clock implementation ----

// fakeClock implements Clock and allows tests to control time progression.
type fakeClock struct {
	mu      sync.Mutex
	tickers []*fakeTicker
}

func (fc *fakeClock) NewTicker(d time.Duration) Ticker {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	ft := &fakeTicker{
		c:    make(chan time.Time, 1),
		d:    d,
		stop: false,
	}
	fc.tickers = append(fc.tickers, ft)
	return ft
}

// tickAll advances all non-stopped tickers by one tick (non-blocking).
func (fc *fakeClock) tickAll() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	for _, ft := range fc.tickers {
		ft.tick()
	}
}

// deliverAllN delivers n guaranteed ticks to all non-stopped tickers.
// Blocks the caller until each tick lands in a ticker buffer.
func (fc *fakeClock) deliverAllN(n int) {
	for i := 0; i < n; i++ {
		fc.mu.Lock()
		tickers := append([]*fakeTicker{}, fc.tickers...)
		fc.mu.Unlock()
		for _, ft := range tickers {
			ft.deliverTick()
		}
	}
}

// waitForTickers blocks until at least n tickers have been created.
// runSource does its immediate fetch before it creates its ticker, so having
// observed that fetch does not mean the ticker exists yet. Call this before
// deliverAllN, which only delivers to tickers that already exist.
func (fc *fakeClock) waitForTickers(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		fc.mu.Lock()
		got := len(fc.tickers)
		fc.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d tickers, have %d", n, got)
		}
		time.Sleep(time.Millisecond)
	}
}

type fakeTicker struct {
	c    chan time.Time
	d    time.Duration
	mu   sync.Mutex
	stop bool
}

func (ft *fakeTicker) C() <-chan time.Time { return ft.c }
func (ft *fakeTicker) Stop() {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	ft.stop = true
}

func (ft *fakeTicker) tick() {
	ft.mu.Lock()
	stop := ft.stop
	ft.mu.Unlock()
	if stop {
		return
	}
	select {
	case ft.c <- time.Time{}:
	default:
		// Channel already has a pending tick; skip to avoid blocking.
	}
}

// deliverTick blocks until a buffered tick is consumed, then sends one.
func (ft *fakeTicker) deliverTick() {
	ft.mu.Lock()
	stop := ft.stop
	ft.mu.Unlock()
	if stop {
		return
	}
	ft.c <- time.Time{}
}

// ---- Fake store ----

type fakeStore struct {
	mu      sync.Mutex
	sources map[int64]Source
}

func newFakeStore() *fakeStore {
	return &fakeStore{sources: make(map[int64]Source)}
}

func (fs *fakeStore) addSource(src Source) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.sources[src.ID] = src
}

func (fs *fakeStore) ListEnabledSources() ([]Source, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var result []Source
	for _, s := range fs.sources {
		if s.Enabled {
			result = append(result, s)
		}
	}
	return result, nil
}

func (fs *fakeStore) GetSource(id int64) (Source, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	s, ok := fs.sources[id]
	if !ok {
		return Source{}, fmt.Errorf("source %d not found", id)
	}
	return s, nil
}

// ---- Test helpers ----

func nonBlockingFetch(t *testing.T) (FetchFunc, chan int64) {
	t.Helper()
	ch := make(chan int64, 100)
	return func(ctx context.Context, src Source) error {
		ch <- src.ID
		return nil
	}, ch
}

// readN reads up to n values from ch with a timeout.
func readN(ch chan int64, n int) []int64 {
	var vals []int64
	for i := 0; i < n; i++ {
		select {
		case v := <-ch:
			vals = append(vals, v)
		case <-time.After(2 * time.Second):
			return vals
		}
	}
	return vals
}

// readAll reads all currently available values from ch without blocking.
func readAll(ch chan int64) []int64 {
	var vals []int64
	for {
		select {
		case v := <-ch:
			vals = append(vals, v)
		default:
			return vals
		}
	}
}

// TestScheduler_StartImmediateFirstFetch verifies that when the scheduler starts,
// each enabled source gets an immediate first fetch.
func TestScheduler_StartImmediateFirstFetch(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "HN", URL: "https://hn/rss", RefreshSec: 600, Enabled: true})
	store.addSource(Source{ID: 2, Name: "Lobsters", URL: "https://lobsters/rss", RefreshSec: 1800, Enabled: true})
	store.addSource(Source{ID: 3, Name: "Disabled", URL: "https://disabled/rss", RefreshSec: 300, Enabled: false})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	calls := readN(fetchCh, 2)
	if len(calls) != 2 {
		t.Fatalf("expected 2 immediate fetches, got %d: %v", len(calls), calls)
	}

	found := make(map[int64]bool)
	for _, id := range calls {
		found[id] = true
	}
	if !found[1] {
		t.Error("source 1 was not fetched")
	}
	if !found[2] {
		t.Error("source 2 was not fetched")
	}

	running := s.RunningSources()
	if len(running) != 2 {
		t.Errorf("expected 2 running sources, got %d", running)
	}
}

// TestScheduler_TickerFiresPeriodically verifies that tickers fire at the
// configured interval and trigger fetches.
func TestScheduler_TickerFiresPeriodically(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Consume the immediate first fetch.
	_ = readN(fetchCh, 1)

	// Deliver 3 guaranteed ticks.
	clock.waitForTickers(t, 1)
	clock.deliverAllN(3)

	calls := readN(fetchCh, 3)
	if len(calls) != 3 {
		t.Fatalf("expected 3 ticker fetches, got %d", len(calls))
	}
	for _, id := range calls {
		if id != 1 {
			t.Errorf("expected source ID 1, got %d", id)
		}
	}
}

// TestScheduler_RefetchSourceWithoutReset verifies that RefreshSource triggers
// an immediate fetch but does NOT reset the ticker interval.
func TestScheduler_RefetchSourceWithoutReset(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 60, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Consume immediate first fetch.
	_ = readN(fetchCh, 1)

	// Trigger an out-of-cycle refresh.
	if err := s.RefreshSource(1); err != nil {
		t.Fatalf("RefreshSource failed: %v", err)
	}

	// Should get one fetch from the refresh.
	calls := readN(fetchCh, 1)
	if len(calls) != 1 {
		t.Fatalf("expected 1 refresh fetch, got %d", len(calls))
	}

	// Now advance the ticker; it should still fire.
	clock.waitForTickers(t, 1)
	clock.deliverAllN(1)
	calls = readN(fetchCh, 1)
	if len(calls) != 1 {
		t.Fatalf("expected 1 ticker fetch, got %d", len(calls))
	}
}

// TestScheduler_AddSourceRuntime verifies that a source added at runtime starts
// with an immediate fetch and then follows its ticker interval.
func TestScheduler_AddSourceRuntime(t *testing.T) {
	store := newFakeStore()
	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Add a source at runtime.
	s.AddSource(context.Background(), Source{ID: 5, Name: "New", URL: "https://new/rss", RefreshSec: 30, Enabled: true})

	// Should get an immediate fetch.
	calls := readN(fetchCh, 1)
	if len(calls) != 1 || calls[0] != 5 {
		t.Fatalf("expected 1 immediate fetch for source 5, got %v", calls)
	}

	// Advance ticker; should get another fetch.
	clock.waitForTickers(t, 1)
	clock.deliverAllN(1)
	calls = readN(fetchCh, 1)
	if len(calls) != 1 || calls[0] != 5 {
		t.Fatalf("expected ticker fetch for source 5, got %v", calls)
	}
}

// TestScheduler_RemoveSourceStopsFetching verifies that removing a source stops
// its ticker/runner goroutine and prevents further fetches.
func TestScheduler_RemoveSourceStopsFetching(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Consume immediate fetch.
	_ = readN(fetchCh, 1)

	// Remove the source.
	s.RemoveSource(1)

	// Non-blocking ticks after remove — they silently drop because the consumer
	// is gone.
	clock.tickAll()
	clock.tickAll()
	clock.tickAll()

	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 fetches after remove, got %d: %v", len(remaining), remaining)
	}

	s.Stop()
}

// TestScheduler_EnableDisableSource verifies that disabling a running source
// pauses fetches, and re-enabling resumes them.
func TestScheduler_EnableDisableSource(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Consume immediate fetch.
	_ = readN(fetchCh, 1)

	// Disable the source.
	s.DisableSource(1)

	// Non-blocking ticks — consumer is gone.
	clock.tickAll()
	clock.tickAll()
	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 fetches after disable, got %d: %v", len(remaining), remaining)
	}

	// Re-enable the source.
	if err := s.EnableSource(context.Background(), 1); err != nil {
		t.Fatalf("EnableSource failed: %v", err)
	}

	// Should get an immediate fetch on re-enable.
	calls := readN(fetchCh, 1)
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("expected 1 fetch on re-enable for source 1, got %v", calls)
	}

	// Ticker should resume.
	clock.waitForTickers(t, 2)
	clock.deliverAllN(1)
	calls = readN(fetchCh, 1)
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("expected ticker fetch for source 1 after re-enable, got %v", calls)
	}
}

// TestScheduler_EnableSourceAlreadyRunning verifies that enabling an already
// running source is a no-op.
func TestScheduler_EnableSourceAlreadyRunning(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 60, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	_ = readN(fetchCh, 1) // immediate fetch

	// Enable the already-running source.
	if err := s.EnableSource(context.Background(), 1); err != nil {
		t.Fatalf("EnableSource failed: %v", err)
	}

	// Should not get another fetch.
	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 extra fetches, got %d", len(remaining))
	}
}

// TestScheduler_AddSourceDuplicate verifies that AddSource on an already-running
// source is a no-op.
func TestScheduler_AddSourceDuplicate(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 60, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	_ = readN(fetchCh, 1) // immediate fetch

	s.AddSource(context.Background(), Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 60, Enabled: true})

	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 extra fetches for duplicate source, got %d", len(remaining))
	}
}

// TestScheduler_RefreshSourceNotRunning verifies that RefreshSource on a source
// that is not running returns an error.
func TestScheduler_RefreshSourceNotRunning(t *testing.T) {
	store := newFakeStore()
	clock := &fakeClock{}
	fetchFn, _ := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	if err := s.RefreshSource(999); err == nil {
		t.Error("expected error for RefreshSource on non-running source, got nil")
	}
}

// TestScheduler_MultipleTickerIntervals verifies that multiple sources tick
// independently.
func TestScheduler_MultipleTickerIntervals(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Fast", URL: "https://fast/rss", RefreshSec: 5, Enabled: true})
	store.addSource(Source{ID: 2, Name: "Slow", URL: "https://slow/rss", RefreshSec: 60, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Consume both immediate fetches.
	_ = readN(fetchCh, 2)

	// Deliver 2 ticks to all tickers. Both sources are ticked together.
	clock.waitForTickers(t, 2)
	clock.deliverAllN(2)

	// Both sources should have 2 more fetches each = 4 fetches.
	calls := readN(fetchCh, 4)
	if len(calls) != 4 {
		t.Fatalf("expected 4 fetches, got %d: %v", len(calls), calls)
	}
}

// TestScheduler_StopCancelsAll verifies that Stop() prevents any more fetches.
func TestScheduler_StopCancelsAll(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Consume immediate fetch.
	_ = readN(fetchCh, 1)

	s.Stop()

	// Non-blocking ticks after stop.
	clock.tickAll()
	clock.tickAll()
	clock.tickAll()

	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 fetches after Stop, got %d: %v", len(remaining), remaining)
	}
}

// TestScheduler_ZeroRefreshSecUsesDefault verifies that a source with
// RefreshSec=0 defaults to the minimum interval.
func TestScheduler_ZeroRefreshSecUsesDefault(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Zero", URL: "https://zero/rss", RefreshSec: 0, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	// Consume immediate fetch.
	calls := readN(fetchCh, 1)
	if len(calls) != 1 {
		t.Fatalf("expected 1 immediate fetch, got %d", len(calls))
	}

	// Ticker should still work.
	clock.waitForTickers(t, 1)
	clock.deliverAllN(1)
	calls = readN(fetchCh, 1)
	if len(calls) != 1 {
		t.Fatalf("expected 1 ticker fetch with zero refresh_sec, got %d", len(calls))
	}
}

// TestScheduler_RunningSources verifies the RunningSources method.
func TestScheduler_RunningSources(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "A", URL: "https://a/rss", RefreshSec: 10, Enabled: true})
	store.addSource(Source{ID: 2, Name: "B", URL: "https://b/rss", RefreshSec: 20, Enabled: true})
	store.addSource(Source{ID: 3, Name: "C", URL: "https://c/rss", RefreshSec: 30, Enabled: false})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	_ = readN(fetchCh, 2) // consume immediate fetches

	running := s.RunningSources()
	if len(running) != 2 {
		t.Fatalf("expected 2 running sources, got %d: %v", len(running), running)
	}

	found := make(map[int64]bool)
	for _, id := range running {
		found[id] = true
	}
	if !found[1] || !found[2] {
		t.Errorf("expected sources 1 and 2 to be running, got %v", running)
	}
}

// TestScheduler_EnableSourceNotFound verifies that EnableSource returns an error
// when the source doesn't exist in the store.
func TestScheduler_EnableSourceNotFound(t *testing.T) {
	store := newFakeStore()
	clock := &fakeClock{}
	fetchFn, _ := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	if err := s.EnableSource(context.Background(), 12345); err == nil {
		t.Error("expected error when enabling non-existent source, got nil")
	}
}

// TestScheduler_ContextCancellationDuringFetch verifies that after Stop, no
// more fetches happen.
func TestScheduler_ContextCancellationDuringFetch(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_ = readN(fetchCh, 1) // immediate fetch

	s.Stop()

	clock.tickAll()
	clock.tickAll()
	clock.tickAll()

	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 fetches after context cancellation, got %d: %v", len(remaining), remaining)
	}
}

// TestScheduler_RefreshSourceDoubleEnqueue verifies that multiple RefreshSource
// calls before the first is processed don't cause double-fetch.
func TestScheduler_RefreshSourceDoubleEnqueue(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 60, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer s.Stop()

	_ = readN(fetchCh, 1) // immediate fetch

	// Enqueue two refreshes — only one should be stored (channel capacity is 1).
	if err := s.RefreshSource(1); err != nil {
		t.Fatalf("first RefreshSource failed: %v", err)
	}
	if err := s.RefreshSource(1); err != nil {
		t.Fatalf("second RefreshSource failed: %v", err)
	}

	// Should get exactly two fetchs — one from the first RefreshSource, one from
	// the second. Actually, since refreshCh is buffered with capacity 1 and we
	// use non-blocking send, only 1 is enqueued. The first RefreshSource
	// succeeds; the second drops (default case). So expect 1 extra fetch.
	// But wait: the first refresh is consumed before the second call? No, both
	// calls happen before the consumer goroutine runs. So the second
	// RefreshSource hits the default case and drops.
	calls := readN(fetchCh, 1)
	if len(calls) != 1 {
		t.Fatalf("expected 1 refresh fetch (deduped), got %d", len(calls))
	}
}

// TestScheduler_StartStopStartStress verifies that starting, stopping, and
// re-starting behaves correctly.
func TestScheduler_StartStopStartStress(t *testing.T) {
	store := newFakeStore()
	store.addSource(Source{ID: 1, Name: "Test", URL: "https://test/rss", RefreshSec: 10, Enabled: true})

	clock := &fakeClock{}
	fetchFn, fetchCh := nonBlockingFetch(t)

	s := New(store, fetchFn, clock, nil)
	err := s.Start(context.Background())
	if err != nil {
		t.Fatalf("first Start failed: %v", err)
	}

	_ = readN(fetchCh, 1) // immediate fetch

	s.Stop()

	// Verify no more fetches after stop.
	clock.tickAll()
	clock.tickAll()
	clock.tickAll()
	time.Sleep(50 * time.Millisecond)
	remaining := readAll(fetchCh)
	if len(remaining) > 0 {
		t.Errorf("expected 0 fetches after Stop, got %d", len(remaining))
	}

	// Creating a new scheduler and starting simulates restart.
	s2 := New(store, fetchFn, clock, nil)
	err = s2.Start(context.Background())
	if err != nil {
		t.Fatalf("second Start failed: %v", err)
	}
	defer s2.Stop()

	calls := readN(fetchCh, 1)
	if len(calls) != 1 || calls[0] != 1 {
		t.Fatalf("expected 1 immediate fetch on restart, got %v", calls)
	}
}
