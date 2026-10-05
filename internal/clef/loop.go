package clef

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Interfaces ──

// Daemon is the part of nyttigd's API the loop uses; *client.Client has it.
type Daemon interface {
	Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error)
	ListSavedViews(ctx context.Context) (*pb.ListSavedViewsResponse, error)
	ListTags(ctx context.Context) (*pb.ListTagsResponse, error)
	ListAssessors(ctx context.Context) (*pb.ListAssessorsResponse, error)
	AddAssessor(ctx context.Context, req *pb.AddAssessorRequest) (*pb.Assessor, error)
	PutAssessment(ctx context.Context, req *pb.PutAssessmentRequest) (*pb.Assessment, error)
}

// Decider asks Clef; *Client has it.
type Decider interface {
	Decide(ctx context.Context, state any, questions map[string]Question) (*Response, error)
}

// ── Errors ──

var (
	// ErrBudgetExhausted ends a pass when daily_tokens is used up.
	ErrBudgetExhausted = errors.New("daily token budget used up")
	// ErrClefAuth ends a pass when Cloudflare rejects the token.
	ErrClefAuth = errors.New("cloudflare rejected the API token")
	// ErrClefUnavailable ends a pass when Clef kept failing with retryable errors.
	ErrClefUnavailable = errors.New("clef unavailable")
)

// ── Tunables ──

const (
	defaultPageSize   = 50
	maxFailedPasses   = 3 // passes in which an item may fail before it is skipped for good
	maxDecideAttempts = 8
	decideBackoffBase = time.Second
	maxBackoff        = 5 * time.Minute
	putAttempts       = 3
	rpcTimeout        = 15 * time.Second
)

var defaultPutBackoff = []time.Duration{500 * time.Millisecond, 2 * time.Second}

// Options configures a Loop.
type Options struct {
	Config  *Config
	Daemon  Daemon
	Decider Decider
	Logger  *slog.Logger
	DryRun  bool // call Clef and log the assessments, write nothing

	// The rest are for tests; zero values give the real behaviour.
	PageSize   int
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Wait       func(ctx context.Context) error // rate limit, called before every Clef request
	PutBackoff []time.Duration
}

// Stats is what one pass did.
type Stats struct {
	Assessed int // items with at least one assessment written (or, in a dry run, scored)
	Skipped  int // items no question covers
	Failed   int // items whose scoring or writing failed
	Calls    int // Clef requests answered
}

// Loop polls a saved view and scores the items it finds.
type Loop struct {
	cfg   *Config
	d     Daemon
	dec   Decider
	log   *slog.Logger
	dry   bool
	page  int
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	wait  func(ctx context.Context) error
	putBO []time.Duration

	assessorID int64

	// State across passes.
	skip     map[int64]bool // items no question covers, or that failed maxFailedPasses passes
	failures map[int64]int
	assessed map[int64]bool // items this process has written assessments for
	warned   bool

	// The daily budget of input tokens, reset at UTC midnight.
	budgetDay  string
	budgetUsed int
}

// New builds a Loop.
func New(o Options) *Loop {
	l := &Loop{
		cfg: o.Config, d: o.Daemon, dec: o.Decider, log: o.Logger, dry: o.DryRun,
		page: o.PageSize, now: o.Now, sleep: o.Sleep, wait: o.Wait, putBO: o.PutBackoff,
		skip: map[int64]bool{}, failures: map[int64]int{}, assessed: map[int64]bool{},
	}
	if l.log == nil {
		l.log = slog.New(slog.DiscardHandler)
	}
	if l.page <= 0 {
		l.page = defaultPageSize
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.sleep == nil {
		l.sleep = sleepCtx
	}
	if l.wait == nil {
		l.wait = newRateLimiter(l.cfg.MaxPerMinute).Wait
	}
	if l.putBO == nil {
		l.putBO = defaultPutBackoff
	}
	return l
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── Rate limit ──

// rateLimiter lets one request through per tick (a small ticker; no new
// dependency for this). The first request does not wait.
type rateLimiter struct {
	mu     sync.Mutex
	every  time.Duration
	ticker *time.Ticker
	primed bool
}

func newRateLimiter(perMinute int) *rateLimiter {
	if perMinute <= 0 {
		perMinute = DefaultMaxPerMinute
	}
	return &rateLimiter{every: time.Minute / time.Duration(perMinute)}
}

// Wait blocks until the next request may go out, or ctx ends.
func (r *rateLimiter) Wait(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.primed {
		r.primed = true
		r.ticker = time.NewTicker(r.every)
		return ctx.Err()
	}
	select {
	case <-r.ticker.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── Startup ──

// EnsureAssessor returns the ID of the assessor called name, creating it when
// missing (the EnsureMe pattern: look up by name, and on AlreadyExists look
// again, so two clients racing is fine). An existing assessor is never changed.
func EnsureAssessor(ctx context.Context, d Daemon, name, description, color string) (int64, error) {
	find := func() (int64, error) {
		resp, err := d.ListAssessors(ctx)
		if err != nil {
			return 0, err
		}
		for _, a := range resp.Assessors {
			if a.Name == name {
				return a.Id, nil
			}
		}
		return 0, nil
	}
	if id, err := find(); err != nil || id != 0 {
		return id, err
	}
	created, err := d.AddAssessor(ctx, &pb.AddAssessorRequest{Name: name, Description: description, Color: color})
	if err == nil {
		return created.Id, nil
	}
	if status.Code(err) != codes.AlreadyExists {
		return 0, err
	}
	id, ferr := find()
	if ferr != nil {
		return 0, ferr
	}
	if id == 0 {
		return 0, fmt.Errorf("could not find or create the assessor %q", name)
	}
	return id, nil
}

// Start finds or creates the assessor and checks the view and the questions'
// tags against the daemon. An unknown view or tag is an error.
func (l *Loop) Start(ctx context.Context) error {
	id, err := EnsureAssessor(ctx, l.d, l.cfg.Assessor, l.cfg.Description, l.cfg.Color)
	if err != nil {
		return fmt.Errorf("assessor %q: %w", l.cfg.Assessor, err)
	}
	l.assessorID = id
	if _, err := l.refresh(ctx); err != nil {
		return err
	}
	l.log.Info("nyttig-clef ready", "assessor", l.cfg.Assessor, "assessor_id", id, "view", l.cfg.View,
		"model", l.cfg.Model, "questions", len(l.cfg.Questions), "dry_run", l.dry)
	return nil
}

// snapshot is what a pass reads from the daemon: the view and tags may have
// been edited since the last one.
type snapshot struct {
	view      *pb.SavedView
	questions []BoundQuestion
	tree      *TagTree
}

func (l *Loop) refresh(ctx context.Context) (*snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	views, err := l.d.ListSavedViews(ctx)
	if err != nil {
		return nil, fmt.Errorf("list views: %w", err)
	}
	view, err := client.FindView(views.Views, l.cfg.View)
	if err != nil {
		return nil, err
	}
	tags, err := l.d.ListTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	qs, err := ResolveQuestions(l.cfg.Questions, tags.Tags)
	if err != nil {
		return nil, err
	}
	snap := &snapshot{view: view, questions: qs, tree: NewTagTree(tags.Tags)}
	if !l.warned {
		l.warned = true
		l.warnAboutView(snap)
	}
	return snap, nil
}

// warnAboutView says so when most items will be skipped: no tag in the view
// and no question that covers an untagged item.
func (l *Loop) warnAboutView(s *snapshot) {
	if f := s.view.GetFilter(); f != nil && f.UnassessedBy != 0 && f.UnassessedBy != l.assessorID {
		l.log.Warn("the view's unassessed filter names another assessor; nyttig-clef uses its own", "view", l.cfg.View)
	}
	if s.view.GetFilter().GetTagId() != 0 {
		return
	}
	for _, q := range s.questions {
		if q.TagID == 0 {
			return
		}
	}
	l.log.Warn("the view has no tag filter and no whole-item question is configured: items without a question's tag will be skipped",
		"view", l.cfg.View)
}

// ── Passes ──

// Run polls until ctx ends. With once it drains the view one time and returns.
func (l *Loop) Run(ctx context.Context, once bool) error {
	for {
		stats, err := l.Pass(ctx)
		if ctx.Err() != nil {
			return nil
		}
		l.log.Info("pass finished", "assessed", stats.Assessed, "skipped", stats.Skipped,
			"failed", stats.Failed, "clef_calls", stats.Calls)
		wait := l.cfg.IntervalDuration
		switch {
		case errors.Is(err, ErrBudgetExhausted):
			l.log.Warn("daily token budget used up, waiting for the next UTC day", "tokens", l.budgetUsed)
			wait = l.untilMidnight()
			err = nil
		case err != nil:
			l.log.Error("pass failed", "error", err)
		}
		if once {
			return err
		}
		if serr := l.sleep(ctx, wait); serr != nil {
			return nil
		}
	}
}

func (l *Loop) untilMidnight() time.Duration {
	now := l.now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return next.Sub(now)
}

// pass is the state of one pass over the view.
type pass struct {
	snap    *snapshot
	seen    map[int64]bool // items handled in this pass that stay in the view
	stats   Stats
	scoring bool // false once the budget ran out
}

// Pass drains the view once: it re-reads the view, then pages through Search.
// An item that was scored leaves the view (it has an assessment now), so the
// next page starts at the number of items left in place so far (offset): the
// skip set, items that failed, and items scored in a dry run.
func (l *Loop) Pass(ctx context.Context) (Stats, error) {
	snap, err := l.refresh(ctx)
	if err != nil {
		return Stats{}, err
	}
	// now is taken once, so the view's window is one cutoff for the whole pass.
	zero := int64(0)
	newest := "newest"
	unassessed := l.assessorID
	req, err := client.ViewSearchRequest(snap.view, client.Overrides{
		AssessorID: &zero, Sort: &newest, NoMinScore: true, UnassessedBy: &unassessed,
	}, l.now())
	if err != nil {
		return Stats{}, fmt.Errorf("view %q: %w", l.cfg.View, err)
	}
	req.Limit = int32(l.page)

	p := &pass{snap: snap, seen: map[int64]bool{}, scoring: true}
	offset := 0
	for {
		if ctx.Err() != nil {
			return p.stats, nil
		}
		req.Offset = int32(offset)
		sctx, cancel := context.WithTimeout(ctx, rpcTimeout)
		resp, err := l.d.Search(sctx, req)
		cancel()
		if err != nil {
			return p.stats, fmt.Errorf("search: %w", err)
		}
		for _, item := range resp.Items {
			if ctx.Err() != nil {
				return p.stats, nil
			}
			if l.skip[item.Id] || p.seen[item.Id] {
				offset++
				continue
			}
			if l.assessed[item.Id] {
				// Scored by us and still in the view: the assessment is out
				// of the view's tag scope. Don't score it again.
				l.log.Warn("the view still returns an item that was assessed; skipping it from now on (the view's tag may not cover the question's tag)",
					"item", item.Id)
				l.skip[item.Id] = true
				offset++
				continue
			}
			stays, err := l.process(ctx, p, item)
			if stays {
				p.seen[item.Id] = true
				offset++
			}
			if err != nil {
				return p.stats, err
			}
		}
		if len(resp.Items) < l.page {
			return p.stats, nil
		}
	}
}

// process scores one item. It reports whether the item stays in the view
// (nothing was written for it); an error ends the pass.
func (l *Loop) process(ctx context.Context, p *pass, item *pb.Item) (stays bool, err error) {
	qs := Applicable(item, p.snap.questions, p.snap.tree)
	if len(qs) == 0 {
		l.skip[item.Id] = true
		p.stats.Skipped++
		l.log.Debug("no question covers the item", "item", item.Id)
		return true, nil
	}
	if l.budgetLeft() == 0 {
		return true, ErrBudgetExhausted
	}

	start := l.now()
	l.log.Debug("scoring", "item", item.Id, "title", item.Title)
	resp, err := l.decide(ctx, p, item, qs)
	if err != nil {
		var pe *PermanentError
		switch {
		case ctx.Err() != nil:
			return true, nil
		case errors.Is(err, ErrBudgetExhausted), errors.Is(err, ErrClefAuth), errors.Is(err, ErrClefUnavailable):
			return true, err
		case errors.As(err, &pe):
			return true, l.fail(p, item, fmt.Errorf("clef: %w", err))
		}
		return true, err
	}
	p.stats.Calls++

	reqs, err := ToAssessments(item, l.assessorID, qs, resp)
	if err != nil {
		return true, l.fail(p, item, err)
	}
	latency := l.now().Sub(start)

	if l.dry {
		l.logAssessed(item, reqs, resp, latency, "dry_run", true)
		l.skip[item.Id] = true // it stays in the view; don't ask again
		p.stats.Assessed++
		return true, nil
	}
	written := 0
	for _, r := range reqs {
		if l.put(ctx, r) {
			written++
		}
	}
	if written == 0 {
		return true, l.fail(p, item, errors.New("no assessment could be written"))
	}
	l.assessed[item.Id] = true
	p.stats.Assessed++
	l.logAssessed(item, reqs, resp, latency, "written", written)
	return false, nil
}

// fail counts a failed pass for the item; after maxFailedPasses it is skipped
// for good. It returns nil: one bad item doesn't end the pass.
func (l *Loop) fail(p *pass, item *pb.Item, cause error) error {
	l.failures[item.Id]++
	p.stats.Failed++
	skipped := l.failures[item.Id] >= maxFailedPasses
	if skipped {
		l.skip[item.Id] = true
	}
	l.log.Warn("item not assessed", "item", item.Id, "error", cause,
		"failed_passes", l.failures[item.Id], "skipped_from_now_on", skipped)
	return nil
}

func (l *Loop) logAssessed(item *pb.Item, reqs []*pb.PutAssessmentRequest, resp *Response, latency time.Duration, k string, v any) {
	ids := make([]string, 0, len(reqs))
	scores := map[string]float64{}
	for _, r := range reqs {
		id := "item"
		if r.TagId != 0 {
			id = fmt.Sprintf("tag.%d", r.TagId)
		}
		ids = append(ids, id)
		scores[id] = *r.Score
	}
	sort.Strings(ids)
	l.log.Info("item assessed", "item", item.Id, "questions", strings.Join(ids, ","), "scores", scores,
		"input_tokens", resp.Usage.InputTokens, "latency_ms", latency.Milliseconds(), k, v)
}

// ── Clef calls ──

// budgetLeft is the input tokens left today, or -1 for no limit.
func (l *Loop) budgetLeft() int {
	limit := l.cfg.MaxDailyTokens()
	if limit == 0 {
		return -1
	}
	if day := l.now().UTC().Format(time.DateOnly); day != l.budgetDay {
		l.budgetDay, l.budgetUsed = day, 0
	}
	return max(limit-l.budgetUsed, 0)
}

// decide asks Clef about the item. Retryable errors wait (Retry-After, else
// 1s doubling, at most 5 minutes) and try again, up to maxDecideAttempts. The
// current item is finished even when a stop signal arrives; only the waits
// between attempts end with ctx.
func (l *Loop) decide(ctx context.Context, p *pass, item *pb.Item, qs []BoundQuestion) (*Response, error) {
	state := BuildState(item, l.cfg.MaxTextChars)
	questions := QuestionMap(qs)
	backoff := decideBackoffBase
	for attempt := 1; ; attempt++ {
		if err := l.wait(ctx); err != nil {
			return nil, err
		}
		resp, err := l.dec.Decide(context.WithoutCancel(ctx), state, questions)
		if err == nil {
			l.budgetLeft() // rolls the day over
			l.budgetUsed += resp.Usage.InputTokens
			return resp, nil
		}
		var re *RetryableError
		var pe *PermanentError
		switch {
		case errors.As(err, &pe) && (pe.Status == 401 || pe.Status == 403):
			return nil, fmt.Errorf("%w: %v", ErrClefAuth, err)
		case !errors.As(err, &re):
			return nil, err
		case attempt >= maxDecideAttempts:
			return nil, fmt.Errorf("%w: %v", ErrClefUnavailable, err)
		}
		d := min(max(re.RetryAfter, backoff), maxBackoff)
		backoff = min(backoff*2, maxBackoff)
		l.log.Warn("clef request failed, backing off", "error", err, "wait", d.String(), "attempt", attempt)
		if err := l.sleep(ctx, d); err != nil {
			return nil, err
		}
	}
}

// put writes one assessment, trying putAttempts times. A failure that cannot
// go away on its own (the daemon refused the request) is not retried. It
// reports whether the assessment was written.
func (l *Loop) put(ctx context.Context, r *pb.PutAssessmentRequest) bool {
	var err error
	for attempt := 0; attempt < putAttempts; attempt++ {
		if attempt > 0 {
			bo := l.putBO[min(attempt-1, len(l.putBO)-1)]
			if serr := l.sleep(context.WithoutCancel(ctx), bo); serr != nil {
				break
			}
		}
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rpcTimeout)
		_, err = l.d.PutAssessment(pctx, r)
		cancel()
		if err == nil {
			return true
		}
		if c := status.Code(err); c == codes.InvalidArgument || c == codes.NotFound {
			break
		}
	}
	l.log.Error("assessment write failed", "item", r.ItemId, "tag", r.TagId, "error", err)
	return false
}
