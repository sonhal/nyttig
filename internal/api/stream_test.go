package api

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func itemMsg(id int64, link string) *pb.ServerMessage {
	return &pb.ServerMessage{Msg: &pb.ServerMessage_Item{Item: &pb.Item{Id: id, Title: "t", Link: link}}}
}

var completeMsg = &pb.ServerMessage{Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}}}
var resetMsg = &pb.ServerMessage{Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}}}

// sseEvent is one parsed Server-Sent Event, or a comment.
type sseEvent struct {
	id, event, data, comment string
}

// readEvents parses SSE frames from r until n events (comments count) or
// the stream ends.
func readEvents(t *testing.T, sc *bufio.Scanner, n int) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	for len(out) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur != (sseEvent{}) {
				out = append(out, cur)
			}
			cur = sseEvent{}
		case strings.HasPrefix(line, ":"):
			cur.comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			cur.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		case strings.HasPrefix(line, "retry: "):
			// Not an event.
		default:
			t.Fatalf("unexpected SSE line %q", line)
		}
	}
	return out
}

func startStreamServer(t *testing.T, fc *fakeClient, cfg Config) *httptest.Server {
	t.Helper()
	cfg.Client = fc
	cfg.Origin = "http://nyttig.test"
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func getStream(t *testing.T, srv *httptest.Server, query string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/stream"+query, nil)
	req.Host = "nyttig.test"
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	return resp, cancel
}

func TestStreamFraming(t *testing.T) {
	fs := newFakeStream(16)
	fc := &fakeClient{stream: fs}
	srv := startStreamServer(t, fc, Config{PingInterval: 50 * time.Millisecond})

	fs.msgs <- itemMsg(9007199254740993, "https://example.com/a")
	fs.msgs <- itemMsg(2, "javascript:alert(1)")
	fs.msgs <- completeMsg

	resp, _ := getStream(t, srv, "?q=rust+lang&source=3&tag=4&sort=oldest&unviewed=true")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream; charset=utf-8",
		"Cache-Control":     "no-store",
		"X-Accel-Buffering": "no",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	f := fs.filter()
	if f == nil || f.Search != "rust lang" || f.SourceId != 3 || f.TagId != 4 || f.Sort != "oldest" || !f.UnviewedOnly {
		t.Errorf("filter sent to daemon = %v", f)
	}

	sc := bufio.NewScanner(resp.Body)
	ev := readEvents(t, sc, 4)
	if len(ev) != 4 {
		t.Fatalf("got %d events: %+v", len(ev), ev)
	}
	if ev[0].event != "reset" || ev[0].data != "{}" {
		t.Errorf("first event = %+v, want reset", ev[0])
	}
	if ev[1].event != "item" || ev[1].id != "9007199254740993" || !strings.Contains(ev[1].data, `"id":"9007199254740993"`) ||
		!strings.Contains(ev[1].data, "https://example.com/a") {
		t.Errorf("item event = %+v", ev[1])
	}
	if ev[2].event != "item" || strings.Contains(ev[2].data, "javascript") {
		t.Errorf("unsafe link not stripped: %+v", ev[2])
	}
	if ev[3].event != "complete" {
		t.Errorf("event 4 = %+v, want complete", ev[3])
	}

	// A live push after complete, then keep-alive pings while idle.
	fs.msgs <- itemMsg(3, "https://example.com/c")
	ev = readEvents(t, sc, 1)
	if len(ev) != 1 || ev[0].event != "item" || ev[0].id != "3" {
		t.Errorf("live push = %+v", ev)
	}
	ev = readEvents(t, sc, 1)
	if len(ev) != 1 || ev[0].comment != "ping" {
		t.Errorf("idle stream sent %+v, want a ping comment", ev)
	}

	// A daemon-side reset (e.g. after reconnect) is passed through.
	fs.msgs <- resetMsg
	ev = readEvents(t, sc, 2)
	if ev[0].event != "reset" && ev[1].event != "reset" {
		t.Errorf("reset not forwarded: %+v", ev)
	}
}

func TestStreamEndsWhenDaemonEnds(t *testing.T) {
	fs := newFakeStream(4)
	fs.msgs <- completeMsg
	fc := &fakeClient{stream: fs}
	srv := startStreamServer(t, fc, Config{})
	resp, _ := getStream(t, srv, "")
	sc := bufio.NewScanner(resp.Body)
	if ev := readEvents(t, sc, 2); len(ev) != 2 || ev[1].event != "complete" {
		t.Fatalf("events = %+v", ev)
	}
	close(fs.msgs)
	done := make(chan struct{})
	go func() {
		for sc.Scan() {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE connection stayed open after the daemon ended the stream")
	}
}

func TestStreamBrowserDisconnectCancelsRPC(t *testing.T) {
	fs := newFakeStream(4)
	fs.msgs <- completeMsg
	fc := &fakeClient{stream: fs}
	srv := startStreamServer(t, fc, Config{})
	resp, cancel := getStream(t, srv, "")
	readEvents(t, bufio.NewScanner(resp.Body), 2)
	cancel()
	select {
	case <-fs.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("gRPC stream context not cancelled after the browser went away")
	}
}

func TestStreamErrors(t *testing.T) {
	// The daemon is down: StreamItems itself fails.
	srv := startStreamServer(t, &fakeClient{streamErr: errUnavailable}, Config{})
	resp, _ := getStream(t, srv, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("daemon down: status %d, want 503", resp.StatusCode)
	}

	// The stream opens but fails before the first message.
	fs := newFakeStream(1)
	fs.recvErr = errUnavailable
	close(fs.msgs)
	srv = startStreamServer(t, &fakeClient{stream: fs}, Config{})
	resp, _ = getStream(t, srv, "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("stream failed: status %d, want 503", resp.StatusCode)
	}

	// Bad filter parameters never reach the daemon.
	srv = startStreamServer(t, &fakeClient{}, Config{})
	resp, _ = getStream(t, srv, "?sort=sideways")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad filter: status %d, want 400", resp.StatusCode)
	}
	resp, _ = getStream(t, srv, "?after=yesterday")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad after: status %d, want 400", resp.StatusCode)
	}
}

// blockingWriter is an http.ResponseWriter whose Write blocks, like a
// browser that stopped reading, until a write deadline in the past is set.
type blockingWriter struct {
	header http.Header

	mu       sync.Mutex
	written  strings.Builder
	block    bool
	deadline chan struct{}
	once     sync.Once
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{header: http.Header{}, deadline: make(chan struct{})}
}

func (w *blockingWriter) Header() http.Header { return w.header }
func (w *blockingWriter) WriteHeader(int)     {}
func (w *blockingWriter) Flush()              {}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	block := w.block
	if !block {
		w.written.Write(p)
	}
	w.mu.Unlock()
	if block {
		<-w.deadline
		return 0, errors.New("i/o timeout")
	}
	return len(p), nil
}

func (w *blockingWriter) SetWriteDeadline(t time.Time) error {
	if !t.IsZero() && t.Before(time.Now()) {
		w.once.Do(func() { close(w.deadline) })
	}
	return nil
}

func (w *blockingWriter) startBlocking() {
	w.mu.Lock()
	w.block = true
	w.mu.Unlock()
}

func TestStreamOverflowClosesConnection(t *testing.T) {
	fs := newFakeStream(0)
	fc := &fakeClient{stream: fs}
	h := &streamHandler{client: fc, pingInterval: time.Hour, queueSize: 4, log: discardLogger()}

	w := newBlockingWriter()
	req := httptest.NewRequest("GET", "/api/stream", nil)
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()

	<-fs.started
	fs.msgs <- completeMsg
	// Wait until the snapshot has been written, then stop "reading".
	deadline := time.Now().Add(5 * time.Second)
	for {
		w.mu.Lock()
		got := strings.Contains(w.written.String(), "event: complete")
		w.mu.Unlock()
		if got {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("snapshot never written")
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.startBlocking()

	// Push more than the queue holds while the writer is stuck. The fake
	// stream is unbuffered, so each send waits for the bridge's reader:
	// sends stop being accepted once the reader has given up.
	sent := 0
	for i := int64(1); i <= 100; i++ {
		select {
		case fs.msgs <- itemMsg(i, ""):
			sent++
		case <-done:
			i = 101
		case <-time.After(time.Second):
			i = 101
		}
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("handler still running after overflow (%d items pushed)", sent)
	}
	if sent > 4+3 {
		t.Errorf("reader accepted %d items with a queue of 4; it should give up on overflow", sent)
	}
	if fs.ctx.Err() == nil {
		t.Error("gRPC stream not cancelled on overflow")
	}
}

// The cutoff the browser fixed for this snapshot reaches the daemon as is.
func TestStreamAfter(t *testing.T) {
	fs := newFakeStream(4)
	fs.msgs <- completeMsg
	srv := startStreamServer(t, &fakeClient{stream: fs}, Config{PingInterval: 50 * time.Millisecond})

	resp, _ := getStream(t, srv, "?after=1788264000&unviewed=1")
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	f := fs.filter()
	if f == nil || f.GetAfter().AsTime().Unix() != 1788264000 || !f.UnviewedOnly {
		t.Errorf("filter sent to daemon = %v", f)
	}
}
