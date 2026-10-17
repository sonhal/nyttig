package tui

import (
	"math"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHeatOf(t *testing.T) {
	cases := []struct {
		score float64
		ok    bool
		want  float64
	}{
		{0, true, 0}, {0.5, true, 0}, {0.6, true, 0.2}, {0.75, true, 0.5}, {1, true, 1},
		{-3, true, 0}, {7, true, 1}, {0.9, false, 0},
	}
	for _, c := range cases {
		if got := heatOf(c.score, c.ok); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("heatOf(%v, %v) = %v, want %v", c.score, c.ok, got, c.want)
		}
	}
}

func TestHeatStep(t *testing.T) {
	for heat, want := range map[float64]int{0.1: 0, 0.25: 0, 0.26: 1, 0.5: 1, 0.51: 2, 0.75: 2, 0.76: 3, 1: 3} {
		if got := heatStep(heat); got != want {
			t.Errorf("heatStep(%v) = %d, want %d", heat, got, want)
		}
	}
	if heatSteps[0] == heatSteps[1] || heatSteps[1] == heatSteps[2] || heatSteps[2] == heatSteps[3] {
		t.Error("heat steps must differ")
	}
}

func heatRow(tb *Table, score float64, viewed, selected bool) string {
	it := scored(1, 1, as(1, "claude", 0, f64(score)))
	it.Title = "Headline"
	it.Link = "https://example.com/x"
	it.Viewed = viewed
	return tb.renderRow(it, selected)
}

func TestRenderRow_HeatBar(t *testing.T) {
	tb := newTestTable(100)
	tb.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}}, 1)
	for _, viewed := range []bool{true, false} {
		for _, selected := range []bool{false, true} {
			hot := stripANSI(heatRow(&tb, 0.9, viewed, selected))
			if !strings.HasPrefix(hot, "▌") {
				t.Errorf("viewed=%v selected=%v: no heat bar: %q", viewed, selected, hot)
			}
			if viewed == strings.Contains(string([]rune(hot)[:3]), "●") {
				t.Errorf("viewed=%v: dot wrong in gutter: %q", viewed, hot)
			}
			if w := lipgloss.Width(hot); w != 100 {
				t.Errorf("hot row is %d wide, want 100", w)
			}
			cold := stripANSI(heatRow(&tb, 0.5, viewed, selected))
			if strings.Contains(cold, "▌") {
				t.Errorf("heat bar at score 0.5: %q", cold)
			}
			if w := lipgloss.Width(cold); w != 100 {
				t.Errorf("cold row is %d wide, want 100", w)
			}
		}
	}
	// The title column starts at the same cell with and without heat.
	hot, cold := stripANSI(heatRow(&tb, 0.9, true, false)), stripANSI(heatRow(&tb, 0.4, true, false))
	if lipgloss.Width(hot[:strings.Index(hot, "Headline")]) != lipgloss.Width(cold[:strings.Index(cold, "Headline")]) {
		t.Errorf("columns misaligned:\n%q\n%q", hot, cold)
	}
}

func TestRenderRow_NoHeatWithoutAssessor(t *testing.T) {
	tb := newTestTable(100)
	tb.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}}, 0)
	if row := stripANSI(heatRow(&tb, 1, true, false)); strings.Contains(row, "▌") {
		t.Errorf("heat bar without a selected assessor: %q", row)
	}
	// Another assessor's score never heats the row.
	tb.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}, {ID: 2, Name: "cvss"}}, 2)
	if row := stripANSI(heatRow(&tb, 1, true, false)); strings.Contains(row, "▌") {
		t.Errorf("heat bar from another assessor's score: %q", row)
	}
}

func TestRenderRow_HeatRespectsTagScope(t *testing.T) {
	tb := newTestTable(100)
	tb.SetAssessors([]AssessorInfo{{ID: 1, Name: "claude"}}, 1)
	it := scored(1, 1, as(1, "claude", 7, f64(0.95)))
	it.Title = "Headline"
	it.Link = "https://example.com/x"
	tb.SetScope(map[int64]bool{3: true})
	if row := stripANSI(tb.renderRow(it, false)); strings.Contains(row, "▌") {
		t.Errorf("heat from an out-of-scope tag assessment: %q", row)
	}
	tb.SetScope(map[int64]bool{7: true})
	if row := stripANSI(tb.renderRow(it, false)); !strings.HasPrefix(row, "▌") {
		t.Errorf("no heat from an in-scope tag assessment: %q", row)
	}
	tb.SetScope(nil)
	if row := stripANSI(tb.renderRow(it, false)); !strings.HasPrefix(row, "▌") {
		t.Errorf("no heat without a tag filter: %q", row)
	}
}
