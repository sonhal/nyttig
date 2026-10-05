package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// In tests lipgloss writes no colors, so the rendered lines are plain text.

func joinLines(ls []string) string { return strings.Join(ls, "\n") }

func hasControl(s string) bool {
	for _, r := range s {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r < 0xa0)) {
			return true
		}
	}
	return false
}

func TestRenderMarkdown_Blocks(t *testing.T) {
	body := "# Title\n\nSome **bold** and *em* and `code`.\n\n- one\n- two\n  - nested\n1. first\n2) second\n\n> quoted\n> > deeper\n\n---\n\n```go\nfunc *x() {}\n# not a heading\n```\nafter"
	got := renderMarkdown(body, nil, 60)
	want := []string{
		"Title",
		"",
		"Some bold and em and code.",
		"",
		"• one",
		"• two",
		"  • nested",
		"1. first",
		"2. second",
		"",
		"│ quoted",
		"│ │ deeper",
		"",
		strings.Repeat("─", 40),
		"",
		"  func *x() {}",
		"  # not a heading",
		"after",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rendered:\n%s\nwant:\n%s", joinLines(got), joinLines(want))
	}
}

func TestRenderMarkdown_LinksAndRefs(t *testing.T) {
	items := []*pb.DigestItem{
		{ItemId: 12, Title: "Kernel \x1b[31mbug", Link: "https://example.com/12"},
		{ItemId: 13, Title: "Bad link", Link: "javascript:alert(1)"},
	}
	for _, tc := range []struct{ in, want string }{
		{"[docs](https://example.com/a) here", "docs (https://example.com/a) here"},
		{"[x](javascript:alert(1)) after", "x after"},
		{"[x](data:text/html,y)", "x"},
		{"[w](https://en.example/Foo_(bar))", "w (https://en.example/Foo_(bar))"},
		{"see https://example.com/page.", "see https://example.com/page."},
		{"nohttps://example.com", "nohttps://example.com"},
		{"see [#12] and [#13] and [#99]", "see [Kernel [31mbug] and [#13] and [#99]"},
		{"<script>alert(1)</script> <b>x</b>", "<script>alert(1)</script> <b>x</b>"},
		{"![alt](https://example.com/i.png)", "!alt (https://example.com/i.png)"},
		{"**open and *open", "**open and *open"},
	} {
		got := joinLines(renderMarkdown(tc.in, items, 80))
		if got != tc.want {
			t.Errorf("%q rendered as %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderMarkdown_NeverEmitsControlCharacters(t *testing.T) {
	hostile := "red \x1b[31mtext\x1b[0m\n\x1b]0;pwned\x07 title\n\x9b2J C1 \u0085 nel\r\ncarriage\u202eevil\u2066x\n\x00nul\n# \x1b[2Jhead\n- \x1b[1mitem\n> \x1b]52;c;x\x07quote\n```\n\x1b[31mcode\x1b[0m\n```\n[\x1b[31mlink](https://example.com/\x1b[0m)\nhttps://example.com/\x1b[0m \xff\xfe"
	for _, w := range []int{20, 40, 120} {
		out := joinLines(renderMarkdown(hostile, []*pb.DigestItem{{ItemId: 1, Title: "t\x1b", Link: "https://example.com"}}, w))
		if hasControl(out) {
			t.Fatalf("width %d: control character in %q", w, out)
		}
		for _, bad := range []string{"\u202e", "\u2066"} {
			if strings.Contains(out, bad) {
				t.Errorf("width %d: %q in output", w, bad)
			}
		}
	}
}

func TestRenderMarkdown_WrapsToWidth(t *testing.T) {
	body := "A paragraph with quite a few words in it that has to be wrapped over several lines.\n\n- a list item with quite a few words in it that wraps too\n\nverylongwordwithoutanyspacesatall_verylongwordwithoutanyspacesatall"
	for _, w := range []int{20, 33, 80} {
		for _, l := range renderMarkdown(body, nil, w) {
			if lipgloss.Width(l) > w {
				t.Errorf("width %d: line %q is %d cells", w, l, lipgloss.Width(l))
			}
		}
	}
	// A list item's continuation lines are indented past the bullet.
	lines := renderMarkdown("- a list item with quite a few words in it that wraps", nil, 20)
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "• ") || !strings.HasPrefix(lines[1], "  ") {
		t.Errorf("list wrap: %q", lines)
	}
	// Wide characters count as two cells.
	for _, l := range renderMarkdown(strings.Repeat("日本語 ", 20), nil, 20) {
		if lipgloss.Width(l) > 20 {
			t.Errorf("wide: %q is %d cells", l, lipgloss.Width(l))
		}
	}
}

func TestRenderMarkdown_HostileInputIsFast(t *testing.T) {
	const size = 64 * 1024
	inputs := map[string]string{
		"stars":      strings.Repeat("*", size),
		"ticks":      strings.Repeat("`", size),
		"brackets":   strings.Repeat("[", size),
		"link opens": strings.Repeat("[a](", size/4),
		"bold opens": strings.Repeat("**a ", size/4),
		"em opens":   strings.Repeat("*a ", size/3),
		"code opens": strings.Repeat("`a ", size/3),
		"refs":       strings.Repeat("[#1", size/3),
		"quotes":     strings.Repeat("> ", size/2),
		"lists":      strings.Repeat("- ", size/2),
		"spaces":     "# a" + strings.Repeat(" ", size) + "b",
		"lines":      strings.Repeat("*a\n", size/3),
		"long url":   "[a](" + strings.Repeat("x", size) + " b)",
		"urls":       strings.Repeat("https://a.example/", size/18),
	}
	for name, in := range inputs {
		start := time.Now()
		renderMarkdown(in, nil, 80)
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s took %v", name, d)
		}
	}
}

// ── The screen ────────────────────────────────────────────────

type fakeDigestAPI struct {
	series  []*pb.DigestSeries
	digests map[int64][]*pb.Digest // series -> newest first
	byID    map[int64]*pb.Digest
	err     error
	calls   []string
}

func (f *fakeDigestAPI) ListDigestSeries(_ context.Context, _ int64) (*pb.ListDigestSeriesResponse, error) {
	f.calls = append(f.calls, "series")
	if f.err != nil {
		return nil, f.err
	}
	return &pb.ListDigestSeriesResponse{Series: f.series}, nil
}

func (f *fakeDigestAPI) ListDigests(_ context.Context, req *pb.ListDigestsRequest) (*pb.ListDigestsResponse, error) {
	f.calls = append(f.calls, fmt.Sprintf("digests %d before %d", req.SeriesId, req.BeforeId))
	all := f.digests[req.SeriesId]
	start := 0
	if req.BeforeId != 0 {
		for i, d := range all {
			if d.Id == req.BeforeId {
				start = i + 1
			}
		}
	}
	rest := all[start:]
	more := false
	if int(req.Limit) > 0 && len(rest) > int(req.Limit) {
		rest, more = rest[:req.Limit], true
	}
	return &pb.ListDigestsResponse{Digests: rest, HasMore: more}, nil
}

func (f *fakeDigestAPI) GetDigest(_ context.Context, id int64) (*pb.Digest, error) {
	f.calls = append(f.calls, fmt.Sprintf("digest %d", id))
	if d, ok := f.byID[id]; ok {
		return d, nil
	}
	return nil, errors.New("not found")
}

func day(d int) *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC))
}

func newFake() *fakeDigestAPI {
	f := &fakeDigestAPI{digests: map[int64][]*pb.Digest{}, byID: map[int64]*pb.Digest{}}
	f.series = []*pb.DigestSeries{
		{Id: 1, AssessorId: 10, AssessorName: "claude", Name: "daily", DigestCount: 3},
		{Id: 2, AssessorId: 20, AssessorName: "gpt", Name: "weekly", DigestCount: 1},
		{Id: 3, AssessorId: 10, AssessorName: "claude", Name: "monthly", DigestCount: 1},
	}
	add := func(series, id int64, title string, d int, inputs ...int64) {
		dg := &pb.Digest{
			Id: id, SeriesId: series, Title: title, Body: "body of " + title,
			PeriodStart: day(d), PeriodEnd: day(d), UpdatedAt: day(d),
			AssessorName: "x", SeriesName: "s",
		}
		for _, in := range inputs {
			dg.Inputs = append(dg.Inputs, &pb.DigestRef{Id: in, Title: f.byID[in].Title, SeriesName: "s", PeriodEnd: day(1)})
		}
		f.byID[id] = dg
		f.digests[series] = append([]*pb.Digest{dg}, f.digests[series]...) // newest first: add oldest first
	}
	add(1, 11, "d1", 1)
	add(1, 12, "d2", 2, 11)
	add(1, 13, "d3", 3, 11)
	add(2, 21, "w1", 5)
	add(3, 31, "m1", 4, 11)
	return f
}

// run executes a command and feeds its message to the screen, until no
// command is left.
func run(s *digestsScreen, cmd tea.Cmd) {
	for cmd != nil {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				run(s, c)
			}
			return
		}
		cmd = s.Update(msg)
	}
}

func dkey(s string) tea.KeyMsg {
	switch s {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func openScreen(t *testing.T, f *fakeDigestAPI) *digestsScreen {
	t.Helper()
	s := newDigestsScreen(f, map[int64]string{10: "#D97757"}, 100, 30)
	run(s, s.Init())
	return s
}

func TestDigestsScreen_OpensTheNewestOfTheFirstSeries(t *testing.T) {
	f := newFake()
	s := openScreen(t, f)
	// Series are listed grouped by assessor: claude's two, then gpt's.
	var names []string
	for _, sr := range s.series {
		names = append(names, sr.Name)
	}
	if strings.Join(names, ",") != "daily,monthly,weekly" {
		t.Fatalf("list order %v", names)
	}
	if s.digest == nil || s.digest.Id != 13 || s.histCursor != 0 || len(s.history) != 3 {
		t.Fatalf("digest %v, cursor %d, history %d", s.digest, s.histCursor, len(s.history))
	}
	v := s.View()
	for _, want := range []string{"[claude]", "[gpt]", "daily", "monthly", "weekly", "d3", "d2", "d1", "body of d3"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "\n") != 29 {
		t.Errorf("view has %d line breaks, want 29 (30 lines)", strings.Count(v, "\n"))
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 100 {
			t.Errorf("line %q is %d cells wide", l, lipgloss.Width(l))
		}
	}
}

func TestDigestsScreen_StepsThroughTheHistory(t *testing.T) {
	s := openScreen(t, newFake())
	run(s, s.handleKey(dkey("j")))
	if s.digest.Id != 12 || s.histCursor != 1 {
		t.Fatalf("after j: digest %d cursor %d", s.digest.Id, s.histCursor)
	}
	run(s, s.handleKey(dkey("down")))
	if s.digest.Id != 11 {
		t.Fatalf("after down: %d", s.digest.Id)
	}
	// At the oldest there is nothing further, and nothing is loaded.
	run(s, s.handleKey(dkey("j")))
	if s.digest.Id != 11 || s.histCursor != 2 {
		t.Fatalf("past the end: digest %d cursor %d", s.digest.Id, s.histCursor)
	}
	run(s, s.handleKey(dkey("k")))
	run(s, s.handleKey(dkey("k")))
	run(s, s.handleKey(dkey("k")))
	if s.digest.Id != 13 || s.histCursor != 0 {
		t.Fatalf("back at the newest: digest %d cursor %d", s.digest.Id, s.histCursor)
	}
	run(s, s.handleKey(dkey("G")))
	if s.digest.Id != 11 {
		t.Errorf("G: %d", s.digest.Id)
	}
	run(s, s.handleKey(dkey("g")))
	if s.digest.Id != 13 {
		t.Errorf("g: %d", s.digest.Id)
	}
}

func TestDigestsScreen_LoadsOlderPagesAtTheEnd(t *testing.T) {
	f := newFake()
	// 45 digests in a series: the first page is 20.
	f.digests[1] = nil
	for i := 45; i >= 1; i-- {
		id := int64(100 + i)
		d := &pb.Digest{Id: id, SeriesId: 1, Title: fmt.Sprintf("n%d", i), Body: "b", PeriodEnd: day(1), UpdatedAt: day(1)}
		f.byID[id] = d
		f.digests[1] = append(f.digests[1], d)
	}
	s := openScreen(t, f)
	if len(s.history) != digestPageSize || !s.hasMore {
		t.Fatalf("first page %d, more %v", len(s.history), s.hasMore)
	}
	s.histCursor = len(s.history) - 1
	run(s, s.loadDigest(s.history[s.histCursor].Id))
	run(s, s.handleKey(dkey("j")))
	if len(s.history) != 2*digestPageSize || s.histCursor != digestPageSize {
		t.Fatalf("after j at the end: history %d, cursor %d", len(s.history), s.histCursor)
	}
	if want := fmt.Sprintf("n%d", 45-digestPageSize); s.digest.Title != want {
		t.Errorf("digest %q, want %q", s.digest.Title, want)
	}
	last := f.calls[len(f.calls)-2]
	if !strings.HasPrefix(last, "digests 1 before ") {
		t.Errorf("calls %v", f.calls)
	}
}

func TestDigestsScreen_MovesBetweenSeriesAndFollowsInputs(t *testing.T) {
	f := newFake()
	s := openScreen(t, f)
	run(s, s.handleKey(dkey("]")))
	if cur := s.current(); cur.Name != "monthly" || s.digest.Id != 31 {
		t.Fatalf("after ]: %v, digest %v", cur, s.digest)
	}
	run(s, s.handleKey(dkey("]")))
	if s.current().Name != "weekly" || s.digest.Id != 21 {
		t.Fatalf("second ]: %v", s.current())
	}
	run(s, s.handleKey(dkey("]"))) // already last
	if s.current().Name != "weekly" {
		t.Errorf("] past the last series moved to %v", s.current())
	}
	run(s, s.handleKey(dkey("[")))
	run(s, s.handleKey(dkey("h")))
	if s.current().Name != "daily" {
		t.Fatalf("back to %v", s.current())
	}

	// Input 1 of d3 is d1 (same series); of m1 it is d1 in another series.
	run(s, s.handleKey(dkey("1")))
	if s.digest.Id != 11 || s.histCursor != 2 {
		t.Fatalf("input 1: digest %d, cursor %d", s.digest.Id, s.histCursor)
	}
	run(s, s.handleKey(dkey("]")))
	run(s, s.handleKey(dkey("1")))
	if s.current().Name != "daily" || s.digest.Id != 11 || s.histCursor != 2 || len(s.history) != 3 {
		t.Fatalf("input from another series: series %v, digest %d, cursor %d", s.current(), s.digest.Id, s.histCursor)
	}
	// An input that does not exist is ignored.
	run(s, s.handleKey(dkey("9")))
	if s.digest.Id != 11 {
		t.Errorf("9 opened %d", s.digest.Id)
	}
}

func TestDigestsScreen_ScrollsTheReadingPane(t *testing.T) {
	f := newFake()
	long := &pb.Digest{Id: 50, SeriesId: 1, Title: "long", Body: strings.Repeat("a line of text\n\n", 60), PeriodEnd: day(9), UpdatedAt: day(9)}
	f.byID[50] = long
	f.digests[1] = []*pb.Digest{long}
	s := newDigestsScreen(f, nil, 100, 20)
	run(s, s.Init())
	if s.scroll != 0 {
		t.Fatal("starts scrolled")
	}
	run(s, s.handleKey(dkey("d")))
	if s.scroll != 9 {
		t.Errorf("d scrolled to %d, want half of 19 rows", s.scroll)
	}
	run(s, s.handleKey(dkey("u")))
	run(s, s.handleKey(dkey("u")))
	if s.scroll != 0 {
		t.Errorf("u past the top: %d", s.scroll)
	}
	for i := 0; i < 100; i++ {
		run(s, s.handleKey(dkey("d")))
	}
	if max := len(s.content()) - s.bodyHeight(); s.scroll != max {
		t.Errorf("scroll %d past the end %d", s.scroll, max)
	}
	// A new digest starts at the top.
	s.scroll = 5
	run(s, s.loadDigest(50))
	if s.scroll != 0 {
		t.Errorf("a new digest kept the scroll: %d", s.scroll)
	}
}

func TestDigestsScreen_ClosesAndReloads(t *testing.T) {
	f := newFake()
	s := openScreen(t, f)
	for _, k := range []string{"q", "esc"} {
		s2 := openScreen(t, f)
		s2.handleKey(dkey(k))
		if !s2.closed {
			t.Errorf("%s did not close the screen", k)
		}
	}
	// r reloads the series and keeps the series that was open.
	run(s, s.handleKey(dkey("]")))
	f.series[0].DigestCount = 99
	run(s, s.handleKey(dkey("r")))
	if s.current().Name != "monthly" || s.series[0].DigestCount != 99 {
		t.Errorf("after r: series %v, count %d", s.current(), s.series[0].DigestCount)
	}
}

func TestDigestsScreen_ShowsErrorsAndDropsStaleAnswers(t *testing.T) {
	f := newFake()
	f.err = errors.New("daemon \x1b[31mgone")
	s := openScreen(t, f)
	v := s.View()
	if !strings.Contains(v, "load series: daemon [31mgone") || hasControl(v) {
		t.Errorf("view with an error: %q", v)
	}
	// An answer for a digest or series that is no longer wanted changes nothing.
	f.err = nil
	s = openScreen(t, f)
	run(s, s.handleKey(dkey("]")))
	before := s.digest.Id
	s.Update(digestMsg{id: 12, digest: f.byID[12]})
	s.Update(digestHistoryMsg{seriesID: 1, digests: f.digests[1]})
	if s.digest.Id != before || len(s.history) != 1 {
		t.Errorf("stale answers applied: digest %d, history %d", s.digest.Id, len(s.history))
	}
}

func TestDigestsScreen_UntrustedTextIsCleaned(t *testing.T) {
	f := newFake()
	f.series[0].Name = "da\x1b[2Jily"
	f.series[0].AssessorName = "cla\x07ude"
	d := f.digests[1][0]
	d.Title = "T\x1b]0;x\x07itle\u202e"
	d.Body = "# H\x1b[31m\nbody \x1b[2J text\x9b"
	d.Items = []*pb.DigestItem{{ItemId: 1, Title: "i\x1b[1m", Link: "https://example.com/\x1b[0m", SourceName: "s\x07"}}
	d.Inputs = []*pb.DigestRef{{Id: 11, Title: "o\x1b[1m", SeriesName: "m\x07", PeriodEnd: day(1)}}
	s := openScreen(t, f)
	for _, w := range []int{40, 100} {
		s.width = w
		s.bodyKey = ""
		if v := s.View(); hasControl(v) || strings.Contains(v, "\u202e") {
			t.Errorf("width %d: control character in the view: %q", w, v)
		}
	}
}

func TestModel_DKeyOpensTheScreenWithoutTouchingTheFeed(t *testing.T) {
	m := NewModel(nil)
	m.width, m.height = 100, 30
	m.assessors = []AssessorInfo{{ID: 10, Name: "claude", Color: "#D97757"}}
	before := m.filter.CurrentSort()
	next, cmd := m.Update(dkey("D"))
	m = asModel(t, next)
	if m.digests == nil || cmd == nil {
		t.Fatalf("D did not open the screen: %v, cmd %v", m.digests, cmd != nil)
	}
	// Feed keys go to the screen: o would cycle the sort in the feed.
	m.digests.api = newFake()
	next, _ = m.Update(dkey("o"))
	m = asModel(t, next)
	if m.filter.CurrentSort() != before {
		t.Error("a key reached the feed while the screen was open")
	}
	if !strings.Contains(m.View(), "loading") {
		t.Errorf("the view is not the screen's: %q", m.View())
	}
	// Answers are routed to it, and q closes it.
	run(m.digests, m.digests.Init())
	if m.digests.digest == nil {
		t.Fatal("no digest loaded")
	}
	next, _ = m.Update(dkey("q"))
	m = asModel(t, next)
	if m.digests != nil {
		t.Error("q did not close the screen")
	}
	// A late answer after closing is dropped without a panic.
	next, cmd = m.Update(digestMsg{id: 1, digest: &pb.Digest{Id: 1}})
	m = asModel(t, next)
	if cmd != nil || m.digests != nil {
		t.Error("a late answer reopened or acted")
	}
	// q on the feed still quits.
	next, cmd = m.Update(dkey("q"))
	if m = asModel(t, next); !m.quitting || cmd == nil {
		t.Error("q on the feed no longer quits")
	}
}
