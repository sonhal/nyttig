// Package tui provides the Nyttig terminal UI components.
//
// filter.go implements the filter bar component: a search input (activated by /),
// source dropdown (cycled by s), tag dropdown (cycled by t), and sort dropdown (cycled by o).
// It emits FilterChangedMsg when any filter parameter changes.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Filter change message ─────────────────────────────────────

// FilterChangedMsg is a Bubble Tea message emitted whenever the filter state
// changes. The TUI model uses this to send a new filter to the gRPC stream.
type FilterChangedMsg struct {
	SourceID     int64
	TagID        int64
	Search       string
	Sort         string
	UnviewedOnly bool
	// AssessorID selects whose scores MinScore and the "score" sort use
	// (0 = none); MinScore is nil for no minimum.
	AssessorID int64
	MinScore   *float64
}

// ── Styles ────────────────────────────────────────────────────

var (
	filterBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(lipgloss.Color("#E0E0E0")).
			Background(lipgloss.Color("#252525"))

	filterLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#808080"))

	filterValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF"))

	filterActiveSearchStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#4EC9B0")).
				Underline(true)
)

// ── Source and tag descriptors ─────────────────────────────────

// SourceInfo describes a source shown in the dropdown.
type SourceInfo struct {
	ID           int64
	Name         string
	Color        string
	Abbreviation string
}

// minScoreSteps are the minimum scores the m key cycles through, after "none".
var minScoreSteps = []float64{0.5, 0.7, 0.9}

// TagInfo describes a tag shown in the dropdown.
type TagInfo struct {
	ID        int64
	Name      string
	Color     string
	ParentIDs []int64 // direct parents; empty = top-level
	Depth     int     // set by SetTags: 0 for a root, 1 for its children, ...
}

// ── FilterBar ──────────────────────────────────────────────────

// FilterBar is the filter bar component: search, source, tag, and sort controls.
type FilterBar struct {
	// Search state.
	searching bool
	search    string

	// Source dropdown.
	sourceIdx int // index into sources slice
	sources   []SourceInfo

	// Tag dropdown.
	tagIdx int // index into tags slice
	tags   []TagInfo
	// tagBelow is how many tags sit below each tag (by ID), for the label.
	tagBelow map[int64]int

	// Sort dropdown.
	sortIdx int
	sorts   []string // "newest", "oldest"; "score" is added once an assessor is selected

	// Assessor dropdown: a virtual "none" first. Its scores are shown first
	// on the rows, and the minimum score and the score sort use them.
	assessorIdx int
	assessors   []AssessorInfo
	// minIdx is 0 for no minimum, else 1 + an index into minScoreSteps.
	minIdx int

	width int
}

// NewFilterBar creates a FilterBar with default sort options.
func NewFilterBar() FilterBar {
	return FilterBar{
		sorts:     []string{"newest", "oldest"},
		assessors: []AssessorInfo{{ID: 0, Name: "none"}},
	}
}

// SetSources replaces the source list. A virtual "all" entry is always first.
func (f *FilterBar) SetSources(sources []SourceInfo) {
	f.sources = []SourceInfo{{ID: 0, Name: "all"}}
	f.sources = append(f.sources, sources...)
	if f.sourceIdx >= len(f.sources) {
		f.sourceIdx = 0
	}
}

// SetTags replaces the tag list. A virtual "all" entry is always first, then
// the tags in tree order (see flattenTagTree).
func (f *FilterBar) SetTags(tags []TagInfo) {
	f.tags = []TagInfo{{ID: 0, Name: "all"}}
	f.tags = append(f.tags, flattenTagTree(tags)...)
	f.tagBelow = tagDescendantCounts(tags)
	if f.tagIdx >= len(f.tags) {
		f.tagIdx = 0
	}
}

// SetAssessors replaces the assessor list. A virtual "none" entry is always
// first. A selected assessor that no longer exists is dropped, with the
// minimum score and the score sort that need it.
func (f *FilterBar) SetAssessors(assessors []AssessorInfo) {
	selected := f.CurrentAssessorID()
	f.assessors = append([]AssessorInfo{{ID: 0, Name: "none"}}, assessors...)
	f.assessorIdx = 0
	for i, a := range f.assessors {
		if a.ID == selected && selected != 0 {
			f.assessorIdx = i
		}
	}
	if f.assessorIdx == 0 {
		f.dropAssessor()
	}
}

// dropAssessor clears what needs an assessor: the minimum score and the
// score sort (back to newest).
func (f *FilterBar) dropAssessor() {
	f.minIdx = 0
	if f.CurrentSort() == "score" {
		f.sortIdx = 0
	}
	f.sorts = []string{"newest", "oldest"}
	if f.sortIdx >= len(f.sorts) {
		f.sortIdx = 0
	}
}

// SetWidth updates the render width.
func (f *FilterBar) SetWidth(w int) {
	f.width = w
}

// ── Keybinding handlers ────────────────────────────────────────

// ToggleSearch activates or deactivates the search input.
func (f *FilterBar) ToggleSearch() {
	f.searching = !f.searching
}

// IsSearching returns whether the search input is currently active.
func (f *FilterBar) IsSearching() bool {
	return f.searching
}

// AppendSearchChar appends a character to the search string.
func (f *FilterBar) AppendSearchChar(ch rune) FilterChangedMsg {
	f.search += string(ch)
	return f.filterMsg()
}

// BackspaceSearch removes the last character from the search string.
func (f *FilterBar) BackspaceSearch() FilterChangedMsg {
	if len(f.search) > 0 {
		f.search = f.search[:len(f.search)-1]
	}
	return f.filterMsg()
}

// ClearSearch clears the search string and exits search mode.
func (f *FilterBar) ClearSearch() FilterChangedMsg {
	f.search = ""
	f.searching = false
	return f.filterMsg()
}

// ConfirmSearch exits search mode but keeps the search text.
func (f *FilterBar) ConfirmSearch() {
	f.searching = false
}

// CycleSource advances to the next source in the dropdown, wrapping around.
func (f *FilterBar) CycleSource() FilterChangedMsg {
	if len(f.sources) == 0 {
		return f.filterMsg()
	}
	f.sourceIdx = (f.sourceIdx + 1) % len(f.sources)
	return f.filterMsg()
}

// CycleTag advances to the next tag in the dropdown, wrapping around.
func (f *FilterBar) CycleTag() FilterChangedMsg {
	if len(f.tags) == 0 {
		return f.filterMsg()
	}
	f.tagIdx = (f.tagIdx + 1) % len(f.tags)
	return f.filterMsg()
}

// CycleAssessor advances to the next assessor ("none" after the last). With
// no assessor the minimum score and the score sort go too.
func (f *FilterBar) CycleAssessor() FilterChangedMsg {
	if len(f.assessors) <= 1 {
		return f.filterMsg()
	}
	f.assessorIdx = (f.assessorIdx + 1) % len(f.assessors)
	if f.assessorIdx == 0 {
		f.dropAssessor()
	} else {
		f.sorts = []string{"newest", "oldest", "score"}
	}
	return f.filterMsg()
}

// CycleMinScore advances the minimum score: none, 0.5, 0.7, 0.9, none. It
// needs an assessor and does nothing without one.
func (f *FilterBar) CycleMinScore() FilterChangedMsg {
	if f.assessorIdx == 0 {
		return f.filterMsg()
	}
	f.minIdx = (f.minIdx + 1) % (len(minScoreSteps) + 1)
	return f.filterMsg()
}

// CycleSort advances to the next sort order.
func (f *FilterBar) CycleSort() FilterChangedMsg {
	if len(f.sorts) == 0 {
		return f.filterMsg()
	}
	f.sortIdx = (f.sortIdx + 1) % len(f.sorts)
	return f.filterMsg()
}

// ── Current filter values ──────────────────────────────────────

// CurrentSourceID returns the currently selected source ID (0 = all).
func (f *FilterBar) CurrentSourceID() int64 {
	if f.sourceIdx < 0 || f.sourceIdx >= len(f.sources) {
		return 0
	}
	return f.sources[f.sourceIdx].ID
}

// CurrentTagID returns the currently selected tag ID (0 = all).
func (f *FilterBar) CurrentTagID() int64 {
	if f.tagIdx < 0 || f.tagIdx >= len(f.tags) {
		return 0
	}
	return f.tags[f.tagIdx].ID
}

// CurrentSort returns the currently selected sort order.
func (f *FilterBar) CurrentSort() string {
	if f.sortIdx < 0 || f.sortIdx >= len(f.sorts) {
		return "newest"
	}
	return f.sorts[f.sortIdx]
}

// CurrentAssessorID returns the selected assessor's ID (0 = none).
func (f *FilterBar) CurrentAssessorID() int64 {
	if f.assessorIdx <= 0 || f.assessorIdx >= len(f.assessors) {
		return 0
	}
	return f.assessors[f.assessorIdx].ID
}

// CurrentMinScore returns the minimum score, nil for none (or no assessor).
func (f *FilterBar) CurrentMinScore() *float64 {
	if f.CurrentAssessorID() == 0 || f.minIdx <= 0 || f.minIdx > len(minScoreSteps) {
		return nil
	}
	v := minScoreSteps[f.minIdx-1]
	return &v
}

// CurrentSearch returns the current search query.
func (f *FilterBar) CurrentSearch() string {
	return f.search
}

// filterMsg builds a FilterChangedMsg from the current state.
func (f *FilterBar) filterMsg() FilterChangedMsg {
	return FilterChangedMsg{
		SourceID:     f.CurrentSourceID(),
		TagID:        f.CurrentTagID(),
		Search:       f.CurrentSearch(),
		Sort:         f.CurrentSort(),
		UnviewedOnly: false,
		AssessorID:   f.CurrentAssessorID(),
		MinScore:     f.CurrentMinScore(),
	}
}

// ── View ───────────────────────────────────────────────────────

// View renders the filter bar.
func (f *FilterBar) View() string {
	parts := []string{}

	// Search segment.
	if f.searching {
		cursor := " "
		if f.search == "" {
			cursor = "█"
		}
		searchSeg := filterActiveSearchStyle.Render("/" + f.search + cursor)
		parts = append(parts, searchSeg)
	} else if f.search != "" {
		searchSeg := filterLabelStyle.Render("/") + filterValueStyle.Render(f.search)
		parts = append(parts, searchSeg)
	}

	// Source dropdown.
	srcName := "all"
	if f.sourceIdx >= 0 && f.sourceIdx < len(f.sources) {
		srcName = f.sources[f.sourceIdx].Name
	}
	srcSeg := fmt.Sprintf("%s [%s]", filterLabelStyle.Render("src:"),
		filterValueStyle.Render(srcName))
	parts = append(parts, srcSeg)

	// Tag dropdown.
	tagName := "all"
	if f.tagIdx >= 0 && f.tagIdx < len(f.tags) {
		tagName = f.tags[f.tagIdx].Name
		// A tag filter also covers the tags below it.
		if n := f.tagBelow[f.tags[f.tagIdx].ID]; n > 0 {
			tagName += fmt.Sprintf(" +%d", n)
		}
	}
	tagSeg := fmt.Sprintf("%s [%s]", filterLabelStyle.Render("tag:"),
		filterValueStyle.Render(tagName))
	parts = append(parts, tagSeg)

	// Assessor dropdown, with its minimum score.
	if f.assessorIdx > 0 && f.assessorIdx < len(f.assessors) {
		name := SanitizeLine(f.assessors[f.assessorIdx].Name)
		if min := f.CurrentMinScore(); min != nil {
			name += " ≥" + formatScore(*min)
		}
		parts = append(parts, fmt.Sprintf("%s [%s]", filterLabelStyle.Render("score:"), filterValueStyle.Render(name)))
	}

	// Sort dropdown.
	sortName := f.CurrentSort()
	sortSeg := fmt.Sprintf("%s [%s]", filterLabelStyle.Render("sort:"),
		filterValueStyle.Render(sortName))
	parts = append(parts, sortSeg)

	joined := strings.Join(parts, "  ")
	return filterBarStyle.Width(f.width).Render(joined)
}
