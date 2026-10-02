package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func f64(v float64) *float64 { return &v }

func as(assessor int64, name string, tag int64, score *float64, note ...string) *pb.Assessment {
	return &pb.Assessment{AssessorId: assessor, AssessorName: name, TagId: tag, Score: score, Note: strings.Join(note, "")}
}

func scored(id int64, hoursAgo int, assessments ...*pb.Assessment) *pb.Item {
	published := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC).Add(-time.Duration(hoursAgo) * time.Hour)
	return &pb.Item{Id: id, Title: "item", Published: tsAt(published), Assessments: assessments}
}

func TestFormatScore(t *testing.T) {
	third := 0.3
	for _, tc := range []struct {
		in   float64
		want string
	}{{0, "0"}, {1, "1"}, {0.9, "0.9"}, {0.95, "0.95"}, {0.123456, "0.12"}, {third + 0.6, "0.9"}} {
		if got := formatScore(tc.in); got != tc.want {
			t.Errorf("formatScore(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScoreScope(t *testing.T) {
	tags := []TagInfo{{ID: 1}, {ID: 2, ParentIDs: []int64{1}}, {ID: 3, ParentIDs: []int64{2}}, {ID: 4}}
	if got := subtree(tags, 0); got != nil {
		t.Errorf("subtree(0) = %v, want nil (no tag filter)", got)
	}
	if got := subtree(tags, 1); !reflect.DeepEqual(got, map[int64]bool{1: true, 2: true, 3: true}) {
		t.Errorf("subtree(1) = %v", got)
	}
	it := scored(1, 0, as(7, "claude", 0, f64(0.5)), as(7, "claude", 3, f64(0.9)), as(7, "claude", 4, f64(0.95)))
	for _, tc := range []struct {
		name  string
		scope map[int64]bool
		want  float64
	}{
		{"no tag filter counts everything", nil, 0.95},
		{"tag 1 counts its subtree and whole-item scores", subtree(tags, 1), 0.9},
		{"tag 4 counts its own", subtree(tags, 4), 0.95},
		{"an unrelated tag still counts whole-item scores", subtree([]TagInfo{{ID: 9}}, 9), 0.5},
	} {
		got, ok := scoreOf(it, 7, tc.scope)
		if !ok || got != tc.want {
			t.Errorf("%s: scoreOf = %v, %v; want %v", tc.name, got, ok, tc.want)
		}
	}
	if _, ok := scoreOf(it, 8, nil); ok {
		t.Error("another assessor's scores were counted")
	}
	if _, ok := scoreOf(scored(1, 0, as(7, "claude", 0, nil, "note")), 7, nil); ok {
		t.Error("a note counted as a score")
	}
	if got, ok := scoreOf(scored(1, 0, as(7, "claude", 0, f64(0))), 7, nil); !ok || got != 0 {
		t.Errorf("a score of 0 = %v, %v", got, ok)
	}
}

func TestScoreChips(t *testing.T) {
	it := scored(1, 0,
		as(2, "zed", 0, f64(0.3)),
		as(1, "claude", 0, f64(0.4)), as(1, "claude", 5, f64(0.8)),
		as(3, "abc", 0, f64(0.1)),
		as(4, "note-only", 0, nil, "n"),
	)
	names := func(selected int64) []string {
		var out []string
		for _, c := range scoreChips(it, selected) {
			out = append(out, c.name+" "+formatScore(c.score))
		}
		return out
	}
	if got, want := names(0), []string{"abc 0.1", "claude 0.8", "zed 0.3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("no selection: %v, want %v", got, want)
	}
	if got, want := names(2), []string{"zed 0.3", "abc 0.1", "claude 0.8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("zed selected: %v, want %v", got, want)
	}
	if len(scoreChips(&pb.Item{}, 0)) != 0 {
		t.Error("chips for an item without assessments")
	}
}

func TestItemOrder(t *testing.T) {
	newer, older := scored(1, 1), scored(2, 5)
	noDate := &pb.Item{Id: 3}
	newest, oldest := itemOrder{sort: "newest"}, itemOrder{sort: "oldest"}
	if !newest.before(newer, older) || newest.before(older, newer) {
		t.Error("newest: the newer item must come first")
	}
	if !oldest.before(older, newer) || oldest.before(newer, older) {
		t.Error("oldest: the older item must come first")
	}
	if !newest.before(older, noDate) || !oldest.before(older, noDate) {
		t.Error("an item without a date goes last in both orders")
	}
	if newest.before(newer, newer) {
		t.Error("an item sorts before itself")
	}

	score := itemOrder{sort: "score", assessorID: 1}
	hi := scored(10, 9, as(1, "c", 0, f64(0.9)))
	lo := scored(11, 1, as(1, "c", 0, f64(0.2)))
	none := scored(12, 0)
	other := scored(13, 0, as(2, "x", 0, f64(1)))
	if !score.before(hi, lo) || score.before(lo, hi) {
		t.Error("score: the higher score must come first")
	}
	if !score.before(lo, none) || score.before(none, lo) {
		t.Error("score: unscored items go last")
	}
	if !score.before(none, scored(14, 3)) || score.before(scored(14, 3), none) {
		t.Error("score: unscored items stay newest first")
	}
	// Another assessor's score is no score: the two are equal (same time),
	// so neither sorts before the other.
	if score.before(other, none) || score.before(none, other) {
		t.Error("score: another assessor's score changed the order")
	}
	// The scope picks which scores count.
	scoped := itemOrder{sort: "score", assessorID: 1, scope: map[int64]bool{5: true}}
	a := scored(20, 1, as(1, "c", 0, f64(0.2)), as(1, "c", 9, f64(0.9)))
	b := scored(21, 1, as(1, "c", 0, f64(0.5)))
	if !scoped.before(b, a) {
		t.Error("an out-of-scope score counted")
	}
	if !score.before(a, b) {
		t.Error("without a scope the best score counts")
	}
}

func TestAssessmentLines_AreOneSanitizedLine(t *testing.T) {
	it := scored(1, 0,
		as(1, "claude\x1b[2J", 3, f64(0.9), "critical\nin Cisco\x1b]0;pwned\x07 IOS"),
		as(2, "cvss", 0, f64(0.98), ""),
		as(3, "note", 99, nil, "only a note"),
	)
	lines := assessmentLines(it, map[int64]string{3: "CVE"})
	want := []string{"claude[2J [CVE] 0.9: critical in Cisco]0;pwned IOS", "cvss 0.98", "note [#99]: only a note"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	for _, l := range lines {
		if strings.ContainsAny(l, "\x1b\x07\n") {
			t.Errorf("line contains control characters: %q", l)
		}
	}
}

func TestRenderRow_ScoreChips(t *testing.T) {
	tb := newTestTable(140)
	tb.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude", Color: "#D97757"}, {ID: 2, Name: "cvss"}}, 2)
	it := scored(1, 1, as(1, "claude", 0, f64(0.9)), as(2, "cvss", 0, f64(0.98)))
	it.Title = "Some headline"
	it.Link = "https://example.com/x"
	row := stripANSI(tb.renderRow(it, false))
	if !strings.Contains(row, "[cvss 0.98] [claude 0.9]") {
		t.Errorf("score chips missing or in the wrong order (cvss is selected): %q", row)
	}
	if !strings.Contains(row, "Some headline") || !strings.Contains(row, "example.com") {
		t.Errorf("row lost its title or domain: %q", row)
	}
	if w := lipgloss.Width(row); w != 140 {
		t.Errorf("row is %d cells wide, want 140", w)
	}
	// No assessments, no chips.
	plain := stripANSI(tb.renderRow(&pb.Item{Title: "x", Link: "https://example.com/x"}, false))
	if strings.Contains(plain, "[claude") {
		t.Errorf("chip on an item without assessments: %q", plain)
	}
}

func TestRenderRow_ScoreChipTextIsUntrusted(t *testing.T) {
	tb := newTestTable(140)
	it := scored(1, 1, as(1, "bad\x1b[2J\x1b]0;x\x07name\n2", 0, f64(0.5)))
	it.Title = "t"
	it.Link = "https://example.com/x"
	row := tb.renderRow(it, false)
	plain := stripANSI(row)
	if strings.Contains(plain, "\n") || strings.Contains(row, "\x1b[2J") || strings.Contains(row, "\x1b]") || strings.Contains(row, "\x07") {
		t.Errorf("injected control characters in the row: %q", row)
	}
	if !strings.Contains(plain, "[bad[2J]0;xname 2 0.5]") {
		t.Errorf("assessor name not flattened to text: %q", plain)
	}
}

func TestTable_ApplyUpdate(t *testing.T) {
	order := itemOrder{sort: "newest"}
	mk := func() Table {
		tb := newTestTable(100)
		tb.SetItems([]*pb.Item{scored(1, 1), scored(3, 3), scored(5, 5)})
		return tb
	}
	ids := func(tb *Table) []int64 { return itemIDs(tb.items) }

	// An item the table shows is replaced in place, keeping a local viewed mark.
	tb := mk()
	tb.items[1].Viewed = true
	tb.ApplyUpdate(scored(3, 3, as(1, "c", 0, f64(0.9))), false, order)
	if !equalIDs(ids(&tb), []int64{1, 3, 5}) || len(tb.items[1].Assessments) != 1 || !tb.items[1].Viewed {
		t.Errorf("replace: ids %v, item %v", ids(&tb), tb.items[1])
	}

	// A matching item that is not shown is inserted in order, and the
	// selection stays on its item.
	tb = mk()
	tb.MoveDown(2)
	tb.ApplyUpdate(scored(2, 2, as(1, "c", 0, f64(0.9))), true, order)
	if !equalIDs(ids(&tb), []int64{1, 2, 3, 5}) {
		t.Errorf("insert: %v", ids(&tb))
	}
	if tb.SelectedItem().Id != 5 {
		t.Errorf("selection moved to item %d, want 5", tb.SelectedItem().Id)
	}

	// One that does not match is ignored; nothing is ever removed.
	tb = mk()
	tb.ApplyUpdate(scored(2, 2), false, order)
	tb.ApplyUpdate(scored(3, 3, as(1, "c", 0, f64(0.1))), false, order)
	if !equalIDs(ids(&tb), []int64{1, 3, 5}) {
		t.Errorf("no match: %v", ids(&tb))
	}

	// At the end, and in an empty table.
	tb = mk()
	tb.ApplyUpdate(scored(9, 9), true, order)
	if !equalIDs(ids(&tb), []int64{1, 3, 5, 9}) {
		t.Errorf("append: %v", ids(&tb))
	}
	empty := newTestTable(100)
	empty.ApplyUpdate(scored(1, 1), true, order)
	if !equalIDs(ids(&empty), []int64{1}) || empty.cursor != 0 {
		t.Errorf("into an empty table: %v cursor %d", ids(&empty), empty.cursor)
	}

	// Score order: placed by score.
	tb = newTestTable(100)
	so := itemOrder{sort: "score", assessorID: 1}
	tb.SetItems([]*pb.Item{scored(1, 1, as(1, "c", 0, f64(0.9))), scored(2, 1, as(1, "c", 0, f64(0.1))), scored(3, 1)})
	tb.ApplyUpdate(scored(4, 1, as(1, "c", 0, f64(0.5))), true, so)
	if !equalIDs(ids(&tb), []int64{1, 4, 2, 3}) {
		t.Errorf("score insert: %v", ids(&tb))
	}
}

// ── Filter bar ──

func TestFilterBar_AssessorCyclingAndMinScore(t *testing.T) {
	f := NewFilterBar()
	f.SetWidth(120)
	f.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}, {ID: 2, Name: "cvss"}})

	if fc := f.CycleMinScore(); fc.MinScore != nil || fc.AssessorID != 0 {
		t.Errorf("a minimum without an assessor: %+v", fc)
	}
	if fc := f.CycleAssessor(); fc.AssessorID != 1 {
		t.Fatalf("first assessor: %+v", fc)
	}
	var seen []string
	for i := 0; i < 5; i++ {
		fc := f.CycleMinScore()
		if fc.MinScore == nil {
			seen = append(seen, "none")
		} else {
			seen = append(seen, formatScore(*fc.MinScore))
		}
	}
	if want := []string{"0.5", "0.7", "0.9", "none", "0.5"}; !reflect.DeepEqual(seen, want) {
		t.Errorf("minimum cycle = %v, want %v", seen, want)
	}
	if v := stripANSI(f.View()); !strings.Contains(v, "score: [claude ≥0.5]") {
		t.Errorf("filter bar lacks the assessor and minimum: %q", v)
	}

	// The score sort becomes available with an assessor.
	var sorts []string
	for i := 0; i < 3; i++ {
		sorts = append(sorts, f.CycleSort().Sort)
	}
	if want := []string{"oldest", "score", "newest"}; !reflect.DeepEqual(sorts, want) {
		t.Errorf("sort cycle = %v, want %v", sorts, want)
	}
	f.CycleSort() // oldest
	f.CycleSort() // score
	if f.CurrentSort() != "score" {
		t.Fatalf("sort = %q", f.CurrentSort())
	}
	// Another assessor keeps the sort; none drops it and the minimum.
	if fc := f.CycleAssessor(); fc.AssessorID != 2 || fc.Sort != "score" {
		t.Errorf("second assessor: %+v", fc)
	}
	fc := f.CycleAssessor()
	if fc.AssessorID != 0 || fc.MinScore != nil || fc.Sort != "newest" {
		t.Errorf("no assessor must clear the minimum and the score sort: %+v", fc)
	}
	if v := stripANSI(f.View()); strings.Contains(v, "score:") {
		t.Errorf("score segment shown without an assessor: %q", v)
	}
	// Without a score sort available, the cycle is newest/oldest again.
	if got := []string{f.CycleSort().Sort, f.CycleSort().Sort}; !reflect.DeepEqual(got, []string{"oldest", "newest"}) {
		t.Errorf("sort cycle without assessor = %v", got)
	}
}

func TestFilterBar_NoAssessorsDoesNothing(t *testing.T) {
	f := NewFilterBar()
	if fc := f.CycleAssessor(); fc.AssessorID != 0 {
		t.Errorf("cycled to %d with no assessors", fc.AssessorID)
	}
}

func TestFilterBar_SetAssessorsDropsAVanishedSelection(t *testing.T) {
	f := NewFilterBar()
	f.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}})
	f.CycleAssessor()
	f.CycleMinScore()
	f.CycleSort()
	f.CycleSort() // score
	// A reload that keeps it changes nothing.
	f.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}, {ID: 2, Name: "cvss"}})
	if f.CurrentAssessorID() != 1 || f.CurrentMinScore() == nil || f.CurrentSort() != "score" {
		t.Errorf("reload changed the selection: %d %v %s", f.CurrentAssessorID(), f.CurrentMinScore(), f.CurrentSort())
	}
	f.SetAssessors([]AssessorInfo{{ID: 2, Name: "cvss"}})
	if f.CurrentAssessorID() != 0 || f.CurrentMinScore() != nil || f.CurrentSort() != "newest" {
		t.Errorf("a removed assessor stayed selected: %d %v %s", f.CurrentAssessorID(), f.CurrentMinScore(), f.CurrentSort())
	}
}

// ── Model ──

func key(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestModel_AssessorKeys(t *testing.T) {
	m := NewModel(nil)
	m.streamStarted = true // no daemon here
	next, _ := m.Update(sourcesLoadedMsg{assessors: []AssessorInfo{{ID: 4, Name: "claude", Color: "#D97757"}}})
	m = asModel(t, next)

	next, cmd := m.Update(key('a'))
	m = asModel(t, next)
	fc, ok := cmd().(FilterChangedMsg)
	if !ok || fc.AssessorID != 4 {
		t.Fatalf("a: %T %+v", cmd(), fc)
	}
	if m.table.selectedAssessor != 4 {
		t.Errorf("the table does not know the selected assessor: %d", m.table.selectedAssessor)
	}
	next, cmd = m.Update(key('m'))
	m = asModel(t, next)
	if fc := cmd().(FilterChangedMsg); fc.MinScore == nil || *fc.MinScore != 0.5 || fc.AssessorID != 4 {
		t.Errorf("m: %+v", fc)
	}
	for _, want := range []string{"oldest", "score"} {
		next, cmd = m.Update(key('o'))
		m = asModel(t, next)
		if got := cmd().(FilterChangedMsg).Sort; got != want {
			t.Errorf("o: sort %q, want %q", got, want)
		}
	}
	next, cmd = m.Update(key('a'))
	m = asModel(t, next)
	if fc := cmd().(FilterChangedMsg); fc.AssessorID != 0 || fc.MinScore != nil || fc.Sort != "newest" {
		t.Errorf("a back to none: %+v", fc)
	}
	if m.table.selectedAssessor != 0 {
		t.Errorf("table still has assessor %d", m.table.selectedAssessor)
	}
}

func TestModel_ItemUpdate(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	m.table.SetItems([]*pb.Item{scored(1, 1), scored(3, 3)})

	// An update for a shown item replaces it, and the model keeps going.
	next, cmd := m.Update(ItemUpdateMsg{Item: scored(3, 3, as(1, "claude", 0, f64(0.8))), Matches: false})
	m = asModel(t, next)
	if cmd != nil {
		t.Errorf("no stream, so no command; got one")
	}
	if len(m.table.items[1].Assessments) != 1 {
		t.Errorf("item not updated: %v", m.table.items[1])
	}
	// One that matches and is new is added in order.
	next, _ = m.Update(ItemUpdateMsg{Item: scored(2, 2, as(1, "claude", 0, f64(0.8))), Matches: true})
	m = asModel(t, next)
	if got := itemIDs(m.table.items); !equalIDs(got, []int64{1, 2, 3}) {
		t.Errorf("ids = %v", got)
	}
	// A nil item is ignored.
	m.Update(ItemUpdateMsg{})
}

func TestModel_InfoLineToggle(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	m.table.SetItems([]*pb.Item{scored(1, 1, as(1, "claude", 0, f64(0.9), "important\nfor the tag"))})
	before := strings.Count(m.View(), "\n")
	if m.tableHeight() != 18 {
		t.Fatalf("table height = %d, want 18", m.tableHeight())
	}

	next, _ = m.Update(key('i'))
	m = asModel(t, next)
	v := stripANSI(m.View())
	if !strings.Contains(v, "claude 0.9: important for the tag") {
		t.Errorf("detail line missing: %q", v)
	}
	// The line takes a row from the table: the window stays the same size.
	if m.tableHeight() != 17 || strings.Count(v, "\n") != before {
		t.Errorf("height %d, lines %d (was %d)", m.tableHeight(), strings.Count(v, "\n"), before)
	}
	for _, line := range strings.Split(v, "\n") {
		if lipgloss.Width(line) > 100 {
			t.Errorf("line wider than the window: %q", line)
		}
	}
	next, _ = m.Update(key('i'))
	m = asModel(t, next)
	if strings.Contains(stripANSI(m.View()), "important") || m.tableHeight() != 18 {
		t.Error("the detail line did not go away")
	}

	// An item without assessments says so.
	m.table.SetItems([]*pb.Item{scored(2, 1)})
	next, _ = m.Update(key('i'))
	m = asModel(t, next)
	if !strings.Contains(stripANSI(m.View()), "no assessments") {
		t.Error("no hint for an item without assessments")
	}
}

func TestListenStream_ItemUpdate(t *testing.T) {
	ch := make(chan *pb.ServerMessage, 2)
	ch <- &pb.ServerMessage{Msg: &pb.ServerMessage_ItemUpdate{ItemUpdate: &pb.Item{Id: 7}}, UpdateMatches: true}
	ch <- &pb.ServerMessage{Msg: &pb.ServerMessage_ItemUpdate{ItemUpdate: &pb.Item{Id: 8}}}
	close(ch)
	sub := client.NewStreamSubForTest(ch, nil)
	for _, want := range []ItemUpdateMsg{{Item: &pb.Item{Id: 7}, Matches: true}, {Item: &pb.Item{Id: 8}}} {
		got, ok := ListenStream(sub)().(ItemUpdateMsg)
		if !ok || got.Item.Id != want.Item.Id || got.Matches != want.Matches {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// ── Rating ──

func TestParseRateText(t *testing.T) {
	for _, tc := range []struct {
		in    string
		score float64
		note  string
	}{
		{"0.8", 0.8, ""},
		{" .5   worth reading ", 0.5, "worth reading"},
		{"1 a\tb  c", 1, "a\tb  c"},
		{"0", 0, ""},
	} {
		score, note, err := parseRateText(tc.in)
		if err != nil || score != tc.score || note != tc.note {
			t.Errorf("parseRateText(%q) = %v, %q, %v; want %v, %q", tc.in, score, note, err, tc.score, tc.note)
		}
	}
	for _, bad := range []string{"", "  ", "1.5", "-1", "high", "0.5x note", "1e-1", "NaN", "Inf"} {
		if _, _, err := parseRateText(bad); err == nil || !strings.Contains(err.Error(), "a score from 0 to 1") {
			t.Errorf("parseRateText(%q) error = %v", bad, err)
		}
	}
}

func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		var msg tea.KeyMsg
		if r == ' ' {
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		} else {
			msg = key(r)
		}
		next, _ := m.Update(msg)
		m = asModel(t, next)
	}
	return m
}

func TestModel_RatingPrompt(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)

	// No item, nothing to rate.
	next, _ = m.Update(key('='))
	m = asModel(t, next)
	if m.rating || !strings.Contains(stripANSI(m.View()), "no item selected") {
		t.Errorf("rating without an item: rating=%v view=%q", m.rating, stripANSI(m.View()))
	}
	// The message goes away with the next key.
	next, _ = m.Update(key('j'))
	m = asModel(t, next)
	if m.note != "" || m.tableHeight() != 18 {
		t.Errorf("note stayed: %q (height %d)", m.note, m.tableHeight())
	}

	m.table.SetItems([]*pb.Item{scored(5, 1)})
	next, _ = m.Update(key('='))
	m = asModel(t, next)
	if !m.rating || m.tableHeight() != 17 {
		t.Fatalf("the prompt did not open: rating=%v height=%d", m.rating, m.tableHeight())
	}
	m = typeText(t, m, "0.7 good one")
	if v := stripANSI(m.View()); !strings.Contains(v, "rate: 0.7 good one█") {
		t.Errorf("prompt text: %q", v)
	}
	// Keys type into the prompt: they are not the TUI's keys.
	if m.quitting || m.showInfo {
		t.Error("a typed q or i acted on the TUI")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = asModel(t, next)
	if m.rateText != "0.7 good on" {
		t.Errorf("backspace: %q", m.rateText)
	}

	// Enter with a good score closes the prompt and starts the RPC.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, next)
	if m.rating || cmd == nil {
		t.Errorf("enter: rating=%v cmd=%v", m.rating, cmd != nil)
	}
	next, _ = m.Update(rateDoneMsg{score: 0.7})
	m = asModel(t, next)
	if !strings.Contains(stripANSI(m.View()), "rated 0.7") {
		t.Errorf("result not shown: %q", stripANSI(m.View()))
	}
	next, _ = m.Update(rateDoneMsg{err: errors.New("daemon\x1b[2J unavailable\n")})
	m = asModel(t, next)
	if v := m.View(); !strings.Contains(stripANSI(v), "rate failed: daemon[2J unavailable") || strings.Contains(v, "\x1b[2J") {
		t.Errorf("failure not shown as text: %q", v)
	}
}

func TestModel_RatingPromptErrorsAndCancel(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	m.table.SetItems([]*pb.Item{scored(5, 1)})
	next, _ = m.Update(key('='))
	m = asModel(t, next)
	m = typeText(t, m, "high")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, next)
	if !m.rating || cmd != nil {
		t.Fatalf("a bad score must keep the prompt open: rating=%v cmd=%v", m.rating, cmd != nil)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "a score from 0 to 1") {
		t.Errorf("no complaint: %q", v)
	}
	// Typing again clears the complaint; Esc leaves without rating.
	m = typeText(t, m, "x")
	if m.rateErr != "" {
		t.Error("complaint stayed after typing")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(t, next)
	if m.rating || cmd != nil || m.tableHeight() != 18 {
		t.Errorf("esc: rating=%v cmd=%v height=%d", m.rating, cmd != nil, m.tableHeight())
	}
}
