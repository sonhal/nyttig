package clef

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Fakes ──

type putKey struct{ item, tag int64 }

// fakeDaemon holds items, tags, views and assessments in memory. Search
// applies the view's unassessed_by the way the daemon does for an assessor
// with no tag scoping: any assessment by that assessor hides the item.
type fakeDaemon struct {
	mu        sync.Mutex
	items     []*pb.Item // newest first
	tags      []*pb.Tag
	views     []*pb.SavedView
	assessors []*pb.Assessor

	puts     map[putKey]*pb.PutAssessmentRequest
	putCalls []*pb.PutAssessmentRequest
	searches []*pb.SearchRequest
	putErr   func(call int, r *pb.PutAssessmentRequest) error // nil = ok
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{puts: map[putKey]*pb.PutAssessmentRequest{}}
}

func (f *fakeDaemon) Search(_ context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.searches = append(f.searches, req)
	var match []*pb.Item
	for _, it := range f.items {
		if req.UnassessedBy != 0 && f.assessedBy(it.Id, req.UnassessedBy) {
			continue
		}
		if req.TagId != 0 && !hasTag(it, req.TagId) {
			continue
		}
		match = append(match, it)
	}
	off := min(int(req.Offset), len(match))
	match = match[off:]
	if req.Limit > 0 && len(match) > int(req.Limit) {
		match = match[:req.Limit]
	}
	return &pb.SearchResponse{Items: match}, nil
}

func hasTag(it *pb.Item, id int64) bool {
	for _, t := range it.Tags {
		if t.Id == id {
			return true
		}
	}
	return false
}

func (f *fakeDaemon) assessedBy(item, assessor int64) bool {
	for k, r := range f.puts {
		if k.item == item && r.AssessorId == assessor {
			return true
		}
	}
	return false
}

func (f *fakeDaemon) ListSavedViews(context.Context) (*pb.ListSavedViewsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &pb.ListSavedViewsResponse{Views: f.views}, nil
}

func (f *fakeDaemon) ListTags(context.Context) (*pb.ListTagsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &pb.ListTagsResponse{Tags: f.tags}, nil
}

func (f *fakeDaemon) ListAssessors(context.Context) (*pb.ListAssessorsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &pb.ListAssessorsResponse{Assessors: f.assessors}, nil
}

func (f *fakeDaemon) AddAssessor(_ context.Context, req *pb.AddAssessorRequest) (*pb.Assessor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := &pb.Assessor{Id: int64(len(f.assessors) + 1), Name: req.Name, Description: req.Description, Color: req.Color}
	f.assessors = append(f.assessors, a)
	return a, nil
}

func (f *fakeDaemon) PutAssessment(_ context.Context, r *pb.PutAssessmentRequest) (*pb.Assessment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls = append(f.putCalls, r)
	if f.putErr != nil {
		if err := f.putErr(len(f.putCalls), r); err != nil {
			return nil, err
		}
	}
	f.puts[putKey{r.ItemId, r.TagId}] = r
	return &pb.Assessment{}, nil
}

func (f *fakeDaemon) put(item, tag int64) *pb.PutAssessmentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts[putKey{item, tag}]
}

// fakeDecider answers every question with the same noul and a fixed score
// unless fn is set.
type fakeDecider struct {
	mu    sync.Mutex
	calls int
	fn    func(call int, state any, qs map[string]Question) (*Response, error)
}

func (d *fakeDecider) Decide(_ context.Context, state any, qs map[string]Question) (*Response, error) {
	d.mu.Lock()
	d.calls++
	n := d.calls
	d.mu.Unlock()
	if d.fn != nil {
		return d.fn(n, state, qs)
	}
	return answerAll(qs, 50), nil
}

func (d *fakeDecider) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// answerAll answers noul questions with 0.8 and score questions with the
// second-highest level.
func answerAll(qs map[string]Question, tokens int) *Response {
	resp := &Response{Answers: map[string]Answer{}, Usage: Usage{InputTokens: tokens}}
	for id, q := range qs {
		switch q.Type {
		case TypeNoul:
			v := 0.8
			resp.Answers[id] = Answer{Type: TypeNoul, Noul: &v}
		case TypeScore:
			lv, _ := q.Criteria.([]string)
			v := float64(len(lv) - 2)
			resp.Answers[id] = Answer{Type: TypeScore, Score: &v}
		}
	}
	return resp
}

// ── Harness ──

const cveTag, secTag = 10, 20

type harness struct {
	t     *testing.T
	d     *fakeDaemon
	dec   *fakeDecider
	loop  *Loop
	logs  *bytes.Buffer
	sleep []time.Duration
	now   time.Time
}

func item(id int64, tags ...int64) *pb.Item {
	it := &pb.Item{Id: id, Title: "item", SourceName: "src", Description: "text"}
	for _, t := range tags {
		it.Tags = append(it.Tags, &pb.Tag{Id: t})
	}
	return it
}

func newHarness(t *testing.T, cfg *Config, mutate func(o *Options)) *harness {
	t.Helper()
	d := newFakeDaemon()
	d.tags = []*pb.Tag{{Id: cveTag, Name: "CVE"}, {Id: secTag, Name: "security"}}
	d.views = []*pb.SavedView{{Id: 1, Name: "clef-inbox", Filter: &pb.ViewFilter{TagId: secTag}}}
	h := &harness{t: t, d: d, dec: &fakeDecider{}, logs: &bytes.Buffer{}, now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	opts := Options{
		Config: cfg, Daemon: d, Decider: h.dec,
		Logger: slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:    func() time.Time { return h.now },
		Sleep: func(ctx context.Context, dur time.Duration) error {
			h.sleep = append(h.sleep, dur)
			return ctx.Err()
		},
		Wait:       func(context.Context) error { return nil },
		PutBackoff: []time.Duration{time.Millisecond},
	}
	if mutate != nil {
		mutate(&opts)
	}
	h.loop = New(opts)
	if err := h.loop.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return h
}

func testConfig(qs ...QuestionSpec) *Config {
	cfg := &Config{
		Assessor: "clef", Description: "d", Color: "#F38020", View: "clef-inbox", Model: "clef-flash",
		MaxPerMinute: 60, MaxTextChars: 100, IntervalDuration: time.Minute, Questions: qs,
	}
	return cfg
}

var (
	wholeQ = QuestionSpec{Type: TypeNoul, Instructions: "relevant?"}
	cveQ   = QuestionSpec{Tag: "CVE", Type: TypeScore, Instructions: "severity?", Levels: []string{"None", "Low", "Medium", "High", "Critical"}}
)

func (h *harness) pass() Stats {
	h.t.Helper()
	st, err := h.loop.Pass(context.Background())
	if err != nil {
		h.t.Fatalf("Pass: %v", err)
	}
	return st
}

// ── Tests ──

func TestPass_WritesMappedAssessments(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ, cveQ), nil)
	h.d.items = []*pb.Item{item(1, secTag, cveTag), item(2, secTag)}
	st := h.pass()
	if st.Assessed != 2 || st.Calls != 2 || st.Failed != 0 {
		t.Fatalf("stats = %+v", st)
	}
	whole := h.d.put(1, 0)
	if whole == nil || *whole.Score != 0.8 || whole.Note != "" || whole.AssessorId != 1 {
		t.Errorf("whole-item assessment = %v", whole)
	}
	cve := h.d.put(1, cveTag)
	if cve == nil || *cve.Score != 0.75 || !strings.HasPrefix(cve.Note, "High (3.0/4)") {
		t.Errorf("CVE assessment = %v", cve)
	}
	if h.d.put(2, cveTag) != nil {
		t.Error("an item without the CVE tag got a CVE assessment")
	}
	if h.d.put(2, 0) == nil {
		t.Error("item 2 got no whole-item assessment")
	}
	// The search asks for unassessed items by this assessor, newest first.
	req := h.d.searches[0]
	if req.UnassessedBy != 1 || req.TagId != secTag || req.Sort != "newest" || req.Limit == 0 {
		t.Errorf("search request = %v", req)
	}
	if !strings.Contains(h.logs.String(), `"msg":"item assessed"`) {
		t.Error("no item assessed log line")
	}
	// A second pass has nothing to do.
	if st := h.pass(); st.Calls != 0 {
		t.Errorf("second pass stats = %+v", st)
	}
}

func TestPass_PartialWriteRetries(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ, cveQ), nil)
	h.d.items = []*pb.Item{item(1, secTag, cveTag)}
	failures := 0
	h.d.putErr = func(call int, r *pb.PutAssessmentRequest) error {
		if r.TagId == cveTag && failures < 2 {
			failures++
			return status.Error(codes.Unavailable, "busy")
		}
		return nil
	}
	if st := h.pass(); st.Assessed != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if h.d.put(1, 0) == nil || h.d.put(1, cveTag) == nil {
		t.Error("both assessments must be written after the retries")
	}
	if n := len(h.d.putCalls); n != 4 { // whole once, CVE three times
		t.Errorf("put calls = %d, want 4", n)
	}
}

func TestPass_PartialWriteGivesUpAndLogs(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ, cveQ), nil)
	h.d.items = []*pb.Item{item(1, secTag, cveTag)}
	h.d.putErr = func(call int, r *pb.PutAssessmentRequest) error {
		if r.TagId == cveTag {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	}
	st := h.pass()
	if st.Assessed != 1 || h.d.put(1, 0) == nil || h.d.put(1, cveTag) != nil {
		t.Fatalf("stats = %+v, whole = %v", st, h.d.put(1, 0))
	}
	if n := len(h.d.putCalls); n != 1+putAttempts {
		t.Errorf("put calls = %d, want %d", n, 1+putAttempts)
	}
	logs := h.logs.String()
	if !strings.Contains(logs, `"msg":"assessment write failed"`) || !strings.Contains(logs, `"tag":10`) {
		t.Errorf("missing the write failure log: %s", logs)
	}
}

func TestPass_RefusedWriteIsNotRetried(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag)}
	h.d.putErr = func(int, *pb.PutAssessmentRequest) error { return status.Error(codes.InvalidArgument, "no") }
	st := h.pass()
	if st.Failed != 1 || len(h.d.putCalls) != 1 {
		t.Errorf("stats = %+v, put calls = %d", st, len(h.d.putCalls))
	}
}

func TestPass_ItemNoQuestionCoversIsSkippedAndPagedPast(t *testing.T) {
	// Only a CVE question and no tag filter in the view: untagged items match
	// the view forever. Page size 1 forces paging past the skipped ones.
	h := newHarness(t, testConfig(cveQ), func(o *Options) { o.PageSize = 1 })
	h.d.views[0].Filter = &pb.ViewFilter{} // no tag filter
	h.loop.warned = false                  // the view changed after Start; warn again
	h.d.items = []*pb.Item{item(1), item(2), item(3, cveTag), item(4)}
	st := h.pass()
	if st.Skipped != 3 || st.Assessed != 1 || h.dec.count() != 1 {
		t.Fatalf("stats = %+v, calls = %d", st, h.dec.count())
	}
	if h.d.put(3, cveTag) == nil {
		t.Error("item 3 was not scored")
	}
	if !strings.Contains(h.logs.String(), "no tag filter and no whole-item question") {
		t.Error("expected the warning for a view with no tag and no whole-item question")
	}
	// Later passes page past the skip set without calling Clef.
	if st := h.pass(); st.Calls != 0 || st.Skipped != 0 {
		t.Errorf("second pass stats = %+v", st)
	}
	// A new item behind the skipped ones is still reached.
	h.d.items = append([]*pb.Item{item(5, cveTag)}, h.d.items...)
	h.d.items = append(h.d.items, item(6, cveTag))
	if st := h.pass(); st.Assessed != 2 {
		t.Errorf("third pass stats = %+v", st)
	}
}

func TestPass_BadAnswerWritesNothingAndIsSkippedAfterThreePasses(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag)}
	h.dec.fn = func(int, any, map[string]Question) (*Response, error) {
		return &Response{Answers: map[string]Answer{}}, nil // missing answer
	}
	for i := 1; i <= 5; i++ {
		st := h.pass()
		if want := map[bool]int{true: 1, false: 0}[i <= maxFailedPasses]; st.Failed != want {
			t.Errorf("pass %d: failed = %d, want %d", i, st.Failed, want)
		}
	}
	if h.dec.count() != maxFailedPasses {
		t.Errorf("clef calls = %d, want %d", h.dec.count(), maxFailedPasses)
	}
	if len(h.d.putCalls) != 0 {
		t.Errorf("writes = %d, want none", len(h.d.putCalls))
	}
	if !strings.Contains(h.logs.String(), `"skipped_from_now_on":true`) {
		t.Error("the third failure must say the item is skipped from now on")
	}
}

func TestPass_ViewEditedBetweenPassesIsPickedUp(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag), item(2, cveTag)}
	h.pass()
	if got := h.d.searches[len(h.d.searches)-1].TagId; got != secTag {
		t.Fatalf("first pass searched tag %d", got)
	}
	h.d.mu.Lock()
	h.d.views[0].Filter = &pb.ViewFilter{TagId: cveTag}
	h.d.mu.Unlock()
	st := h.pass()
	if got := h.d.searches[len(h.d.searches)-1].TagId; got != cveTag {
		t.Errorf("second pass searched tag %d, want the edited view's %d", got, cveTag)
	}
	if st.Assessed != 1 || h.d.put(2, 0) == nil {
		t.Errorf("stats = %+v", st)
	}
}

func TestPass_ViewWindowBecomesCutoff(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.views[0].Filter = &pb.ViewFilter{TagId: secTag, Since: "2d"}
	h.pass()
	req := h.d.searches[0]
	if req.After == nil || !req.After.AsTime().Equal(h.now.Add(-48*time.Hour)) {
		t.Errorf("after = %v", req.After)
	}
}

func TestPass_DryRunWritesNothing(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ, cveQ), func(o *Options) { o.DryRun = true })
	h.d.items = []*pb.Item{item(1, secTag, cveTag), item(2, secTag)}
	st := h.pass()
	if st.Assessed != 2 || h.dec.count() != 2 {
		t.Fatalf("stats = %+v, calls = %d", st, h.dec.count())
	}
	if len(h.d.putCalls) != 0 {
		t.Errorf("writes = %d, want none", len(h.d.putCalls))
	}
	if !strings.Contains(h.logs.String(), `"dry_run":true`) {
		t.Error("the dry run must log what it would write")
	}
	// The items stay in the view; they are not asked about again.
	if st := h.pass(); st.Calls != 0 {
		t.Errorf("second pass stats = %+v", st)
	}
}

func TestPass_DailyBudgetStopsCalls(t *testing.T) {
	cfg := testConfig(wholeQ)
	limit := 100
	cfg.DailyTokens = &limit
	h := newHarness(t, cfg, nil)
	h.dec.fn = func(_ int, _ any, qs map[string]Question) (*Response, error) { return answerAll(qs, 60), nil }
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag), item(3, secTag)}
	st, err := h.loop.Pass(context.Background())
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want ErrBudgetExhausted", err)
	}
	if st.Calls != 2 || h.dec.count() != 2 {
		t.Errorf("clef calls = %d, want 2 (60 + 60 tokens passes the 100 limit)", h.dec.count())
	}
	// Still the same day: no calls.
	if _, err := h.loop.Pass(context.Background()); !errors.Is(err, ErrBudgetExhausted) || h.dec.count() != 2 {
		t.Errorf("err = %v, calls = %d", err, h.dec.count())
	}
	// At UTC midnight the budget resets.
	h.now = time.Date(2026, 10, 6, 0, 0, 1, 0, time.UTC)
	if st := h.pass(); st.Calls != 1 {
		t.Errorf("next day stats = %+v", st)
	}
}

func TestPass_ZeroDailyTokensIsNoLimit(t *testing.T) {
	cfg := testConfig(wholeQ)
	zero := 0
	cfg.DailyTokens = &zero
	h := newHarness(t, cfg, nil)
	h.dec.fn = func(_ int, _ any, qs map[string]Question) (*Response, error) { return answerAll(qs, 1_000_000), nil }
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag), item(3, secTag)}
	if st := h.pass(); st.Calls != 3 {
		t.Errorf("stats = %+v", st)
	}
}

func TestPass_RateLimitBackoff(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag)}
	h.dec.fn = func(call int, _ any, qs map[string]Question) (*Response, error) {
		switch call {
		case 1, 2:
			return nil, &RetryableError{Status: 429, Msg: "slow down"}
		case 3:
			return nil, &RetryableError{Status: 429, RetryAfter: 10 * time.Second, Msg: "slow down"}
		case 4:
			return nil, &RetryableError{Status: 429, RetryAfter: time.Hour, Msg: "slow down"}
		}
		return answerAll(qs, 10), nil
	}
	st := h.pass()
	if st.Assessed != 2 {
		t.Fatalf("stats = %+v", st)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 10 * time.Second, maxBackoff}
	if len(h.sleep) != len(want) {
		t.Fatalf("sleeps = %v, want %v", h.sleep, want)
	}
	for i := range want {
		if h.sleep[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v", i, h.sleep[i], want[i])
		}
	}
}

func TestPass_ClefUnavailableEndsThePass(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag)}
	h.dec.fn = func(int, any, map[string]Question) (*Response, error) {
		return nil, &RetryableError{Status: 503, Msg: "down"}
	}
	_, err := h.loop.Pass(context.Background())
	if !errors.Is(err, ErrClefUnavailable) || h.dec.count() != maxDecideAttempts {
		t.Errorf("err = %v, calls = %d", err, h.dec.count())
	}
	for _, d := range h.sleep {
		if d > maxBackoff {
			t.Errorf("slept %v, over the cap", d)
		}
	}
}

func TestPass_RejectedTokenEndsThePass(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag)}
	h.dec.fn = func(int, any, map[string]Question) (*Response, error) {
		return nil, &PermanentError{Status: 401, Msg: "bad token"}
	}
	_, err := h.loop.Pass(context.Background())
	if !errors.Is(err, ErrClefAuth) || h.dec.count() != 1 {
		t.Errorf("err = %v, calls = %d", err, h.dec.count())
	}
}

func TestPass_PermanentClefErrorFailsOnlyThatItem(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag)}
	h.dec.fn = func(call int, _ any, qs map[string]Question) (*Response, error) {
		if call == 1 {
			return nil, &PermanentError{Status: 400, Msg: "bad input"}
		}
		return answerAll(qs, 5), nil
	}
	st := h.pass()
	if st.Failed != 1 || st.Assessed != 1 || h.d.put(2, 0) == nil {
		t.Errorf("stats = %+v", st)
	}
}

func TestPass_StateAndQuestionsSent(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ, cveQ), nil)
	h.d.items = []*pb.Item{item(1, secTag, cveTag)}
	h.d.items[0].Tags[0].Name = "security"
	var gotState any
	var gotQs map[string]Question
	h.dec.fn = func(_ int, s any, qs map[string]Question) (*Response, error) {
		gotState, gotQs = s, qs
		return answerAll(qs, 1), nil
	}
	h.pass()
	st, _ := gotState.(map[string]any)
	if st["title"] != "item" || st["source"] != "src" || st["text"] != "text" {
		t.Errorf("state = %v", gotState)
	}
	if _, ok := gotQs["item"]; !ok {
		t.Errorf("questions = %v", gotQs)
	}
	if _, ok := gotQs["tag.10"]; !ok {
		t.Errorf("questions = %v", gotQs)
	}
}

func TestPass_StopSignalFinishesCurrentItem(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag), item(3, secTag)}
	ctx, cancel := context.WithCancel(context.Background())
	h.dec.fn = func(call int, _ any, qs map[string]Question) (*Response, error) {
		cancel() // the signal arrives while the first item is being scored
		return answerAll(qs, 1), nil
	}
	st, err := h.loop.Pass(ctx)
	if err != nil || st.Assessed != 1 || h.d.put(1, 0) == nil || h.d.put(2, 0) != nil {
		t.Errorf("stats = %+v, err = %v", st, err)
	}
}

func TestRun_Once(t *testing.T) {
	h := newHarness(t, testConfig(wholeQ), nil)
	h.d.items = []*pb.Item{item(1, secTag)}
	if err := h.loop.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if h.d.put(1, 0) == nil || len(h.sleep) != 0 {
		t.Errorf("put = %v, sleeps = %v", h.d.put(1, 0), h.sleep)
	}
}

func TestRun_PollsUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	h := newHarness(t, testConfig(wholeQ), func(o *Options) {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			if d != time.Minute {
				t.Errorf("slept %v, want the interval", d)
			}
			if n++; n == 2 {
				cancel()
			}
			return ctx.Err()
		}
	})
	h.d.items = []*pb.Item{item(1, secTag)}
	if err := h.loop.Run(ctx, false); err != nil {
		t.Fatal(err)
	}
	if n != 2 || h.d.put(1, 0) == nil {
		t.Errorf("sleeps = %d", n)
	}
}

func TestRun_BudgetWaitsUntilMidnight(t *testing.T) {
	cfg := testConfig(wholeQ)
	limit := 1
	cfg.DailyTokens = &limit
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var slept []time.Duration
	h := newHarness(t, cfg, func(o *Options) {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			slept = append(slept, d)
			cancel()
			return ctx.Err()
		}
	})
	h.d.items = []*pb.Item{item(1, secTag), item(2, secTag)}
	_ = h.loop.Run(ctx, false)
	if len(slept) != 1 || slept[0] != 12*time.Hour {
		t.Errorf("slept = %v, want 12h to UTC midnight", slept)
	}
}

func TestStart_Errors(t *testing.T) {
	t.Run("unknown view", func(t *testing.T) {
		d := newFakeDaemon()
		l := New(Options{Config: testConfig(wholeQ), Daemon: d, Decider: &fakeDecider{}})
		if err := l.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "view") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unknown tag", func(t *testing.T) {
		d := newFakeDaemon()
		d.views = []*pb.SavedView{{Id: 1, Name: "clef-inbox"}}
		l := New(Options{Config: testConfig(cveQ), Daemon: d, Decider: &fakeDecider{}})
		if err := l.Start(context.Background()); err == nil || !strings.Contains(err.Error(), `"CVE"`) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestEnsureAssessor(t *testing.T) {
	ctx := context.Background()
	t.Run("creates", func(t *testing.T) {
		d := newFakeDaemon()
		id, err := EnsureAssessor(ctx, d, "clef", "desc", "#fff")
		if err != nil || id != 1 || len(d.assessors) != 1 {
			t.Errorf("id = %d, err = %v", id, err)
		}
	})
	t.Run("finds and never overwrites", func(t *testing.T) {
		d := newFakeDaemon()
		d.assessors = []*pb.Assessor{{Id: 7, Name: "clef", Description: "mine"}}
		id, err := EnsureAssessor(ctx, d, "clef", "other", "#000")
		if err != nil || id != 7 || d.assessors[0].Description != "mine" {
			t.Errorf("id = %d, err = %v", id, err)
		}
	})
	t.Run("lost race", func(t *testing.T) {
		d := &raceDaemon{fakeDaemon: newFakeDaemon()}
		id, err := EnsureAssessor(ctx, d, "clef", "d", "c")
		if err != nil || id != 9 {
			t.Errorf("id = %d, err = %v", id, err)
		}
	})
}

// raceDaemon: another client creates the assessor between our lookup and add.
type raceDaemon struct {
	*fakeDaemon
	added bool
}

func (r *raceDaemon) ListAssessors(context.Context) (*pb.ListAssessorsResponse, error) {
	if r.added {
		return &pb.ListAssessorsResponse{Assessors: []*pb.Assessor{{Id: 9, Name: "clef"}}}, nil
	}
	return &pb.ListAssessorsResponse{}, nil
}

func (r *raceDaemon) AddAssessor(context.Context, *pb.AddAssessorRequest) (*pb.Assessor, error) {
	r.added = true
	return nil, status.Error(codes.AlreadyExists, "exists")
}

func TestRateLimiter(t *testing.T) {
	r := newRateLimiter(6000) // one per 10ms
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := r.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if el := time.Since(start); el < 25*time.Millisecond {
		t.Errorf("4 requests took %v, want at least 3 ticks", el)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.Wait(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}
