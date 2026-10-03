// Package tui provides the Nyttig terminal UI components.
//
// table.go implements a custom Lipgloss table renderer for the news item list.
// It renders a scrollable viewport with unviewed indicators, colored tag chips,
// a published date, truncated titles and descriptions, right-aligned domains,
// and row highlighting.
package tui

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Styles ────────────────────────────────────────────────────

var (
	unviewedDot = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4EC9B0")).
			Bold(true).
			Render("●")

	rowSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("#3A3D41"))

	rowNormal = lipgloss.NewStyle()

	timeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808080"))

	// fetchedTimeStyle marks a fetch time shown in place of a missing
	// published date.
	fetchedTimeStyle = timeStyle.Italic(true)

	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6A9955"))

	domainStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#569CD6"))

	tagBracketStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808080"))
)

// dateColumnWidth is the fixed width of the published-date column ("dd.MM HH:mm").
const dateColumnWidth = 11

// ── Table ──────────────────────────────────────────────────────

// sourceDisplay holds display metadata for a single source, used to render
// source chips in the table.
type sourceDisplay struct {
	Name  string // abbreviation if set, full name otherwise
	Color string // hex color, empty means default gray
}

// Table renders a scrollable viewport of news items using custom Lipgloss styles.
type Table struct {
	items  []*pb.Item
	cursor int // selected row index (0-based)
	offset int // first visible row index (scroll position)

	width  int
	height int // number of visible rows

	// Tag lookup maps tag name to color (from status bar context).
	tagColors map[string]string

	// sourceMeta maps source_id to display info (abbreviation or name, plus color).
	sourceMeta map[int64]sourceDisplay

	// assessors maps assessor_id to its current name and color for the score
	// chips; selectedAssessor's chip comes first.
	assessors        map[int64]AssessorInfo
	selectedAssessor int64
}

// NewTable creates a Table with the given dimensions.
func NewTable() Table {
	return Table{
		tagColors: make(map[string]string),
	}
}

// SetItems replaces the item list and resets cursor/offset.
func (t *Table) SetItems(items []*pb.Item) {
	t.items = items
	if t.cursor >= len(t.items) {
		t.cursor = max(0, len(t.items)-1)
	}
	t.offset = 0
}

// AppendItems adds items to the end of the list.
func (t *Table) AppendItems(items []*pb.Item) {
	t.items = append(t.items, items...)
}

// PrependItems adds items to the start of the list. If the user is at the
// very top, the view stays there so the new items become visible; otherwise
// the cursor and scroll offset shift so the selection stays on the same item.
func (t *Table) PrependItems(items []*pb.Item) {
	if len(items) == 0 {
		return
	}
	t.items = append(append(make([]*pb.Item, 0, len(items)+len(t.items)), items...), t.items...)
	if t.cursor == 0 && t.offset == 0 {
		return
	}
	t.cursor += len(items)
	t.offset += len(items)
}

// GetItems returns all items currently in the table.
func (t *Table) GetItems() []*pb.Item {
	return t.items
}

// SelectedItem returns the currently selected item, or nil if empty.
func (t *Table) SelectedItem() *pb.Item {
	if len(t.items) == 0 || t.cursor >= len(t.items) {
		return nil
	}
	return t.items[t.cursor]
}

// SetSize updates the renderable area dimensions.
func (t *Table) SetSize(width, height int) {
	t.width = width
	t.height = height
}

// SetTagColors sets the color lookup for tag chip rendering.
func (t *Table) SetTagColors(colors map[string]string) {
	t.tagColors = colors
}

// SetAssessors sets the assessor lookup for score chip rendering.
func (t *Table) SetAssessors(list []AssessorInfo, selected int64) {
	t.assessors = make(map[int64]AssessorInfo, len(list))
	for _, a := range list {
		t.assessors[a.ID] = a
	}
	t.selectedAssessor = selected
}

// ApplyUpdate applies an item whose assessments changed. An item the table
// shows is replaced where it is (and keeps a viewed mark the daemon has not
// heard of yet). One it does not show is inserted in order, but only when
// the daemon says it matches the filter. Nothing is ever removed: a reset
// resyncs.
func (t *Table) ApplyUpdate(item *pb.Item, matches bool, order itemOrder) {
	for i, cur := range t.items {
		if cur.Id == item.Id {
			item.Viewed = item.Viewed || cur.Viewed
			t.items[i] = item
			return
		}
	}
	if !matches {
		return
	}
	at := len(t.items)
	for i, cur := range t.items {
		if order.before(item, cur) {
			at = i
			break
		}
	}
	t.items = append(t.items, nil)
	copy(t.items[at+1:], t.items[at:])
	t.items[at] = item
	// Keep the selection on the same item.
	if len(t.items) > 1 && at <= t.cursor {
		t.cursor++
	}
	if at < t.offset {
		t.offset++
	}
	t.scrollToCursor()
}

// SetSourceMeta sets the source metadata lookup for source chip rendering.
func (t *Table) SetSourceMeta(meta map[int64]sourceDisplay) {
	t.sourceMeta = meta
}

// MoveDown moves the selection down by n rows.
func (t *Table) MoveDown(n int) {
	if len(t.items) == 0 {
		return
	}
	t.cursor = min(t.cursor+n, len(t.items)-1)
	t.scrollToCursor()
}

// MoveUp moves the selection up by n rows.
func (t *Table) MoveUp(n int) {
	t.cursor = max(t.cursor-n, 0)
	t.scrollToCursor()
}

// GoToTop jumps to the first item.
func (t *Table) GoToTop() {
	t.cursor = 0
	t.offset = 0
}

// GoToBottom jumps to the last item.
func (t *Table) GoToBottom() {
	if len(t.items) == 0 {
		return
	}
	t.cursor = len(t.items) - 1
	t.scrollToCursor()
}

// scrollToCursor adjusts offset so the cursor is visible.
func (t *Table) scrollToCursor() {
	if t.height <= 0 || len(t.items) == 0 {
		return
	}
	// Cursor above viewport.
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	// Cursor below viewport.
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	// Clamp offset.
	if t.offset < 0 {
		t.offset = 0
	}
	maxOffset := max(0, len(t.items)-t.height)
	if t.offset > maxOffset {
		t.offset = maxOffset
	}
}

// VisibleItemIDs returns the IDs of items currently in the viewport.
func (t *Table) VisibleItemIDs() []int64 {
	if t.height <= 0 || len(t.items) == 0 {
		return nil
	}
	end := min(t.offset+t.height, len(t.items))
	ids := make([]int64, 0, end-t.offset)
	for i := t.offset; i < end; i++ {
		ids = append(ids, t.items[i].Id)
	}
	return ids
}

// View renders the table into a styled string.
func (t *Table) View() string {
	if t.width <= 0 || t.height <= 0 {
		return ""
	}
	if len(t.items) == 0 {
		return lipgloss.NewStyle().
			Width(t.width).
			Height(t.height).
			Align(lipgloss.Center, lipgloss.Center).
			Foreground(lipgloss.Color("#808080")).
			Render("no items — waiting for feeds...")
	}

	end := min(t.offset+t.height, len(t.items))
	rows := make([]string, 0, end-t.offset)

	for i := t.offset; i < end; i++ {
		item := t.items[i]
		row := t.renderRow(item, i == t.cursor)
		rows = append(rows, row)
	}

	// Pad with empty rows if fewer items than viewport height.
	for len(rows) < t.height {
		rows = append(rows, strings.Repeat(" ", t.width))
	}

	return strings.Join(rows, "\n")
}

// renderRow renders a single item row. If selected, applies highlight styling.
func (t *Table) renderRow(item *pb.Item, selected bool) string {
	// Layout (columns):
	//   viewed (2) | date (11) | tags (variable) | title + description (fill) | domain (right)
	const (
		margin   = 1
		minTitle = 20
		minDesc  = 12
	)

	// 1. Viewed indicator.
	viewedCol := "  "
	if !item.Viewed {
		viewedCol = unviewedDot + " "
	}

	// 2. Date: the published date, else the fetch time in italics (the
	// order the daemon sorts by is unchanged: such items still go last).
	// Blank-padded when both are unknown so columns stay aligned.
	dateCol := timeStyle.Render(formatDate(item.Published))
	if item.Published == nil && item.FetchedAt != nil {
		dateCol = fetchedTimeStyle.Render(formatDate(item.FetchedAt))
	}

	// 3. Source chip.
	sourceCol := t.renderSourceChip(item.SourceId)

	// 4. Tags, then the assessors' scores.
	tagsCol := t.renderTags(item.Tags)
	if scores := t.renderScores(item); scores != "" {
		if tagsCol != "" {
			tagsCol += " "
		}
		tagsCol += scores
	}

	// 5. Title + description + domain.
	// viewed(2) + space(1) + date(11) + space(1) + source(variable) + space(1) + tags(variable) + space(1) + ...
	prefixLen := 2 + margin + dateColumnWidth + margin
	sourceLen := lipgloss.Width(sourceCol)
	if sourceLen > 0 {
		sourceLen += margin
	}
	tagsLen := lipgloss.Width(tagsCol)
	if tagsLen > 0 {
		tagsLen += margin
	}

	// Domain (the source) from link, right-aligned.
	domain := extractDomain(item.Link)
	domainCol := domainStyle.Render(domain)
	domainLen := lipgloss.Width(domainCol)

	// Space available for the title and description, before the right-aligned domain.
	avail := t.width - prefixLen - sourceLen - tagsLen - margin - domainLen
	if avail < 5 {
		avail = 5
	}

	// The description fills whatever space the title leaves, and is truncated
	// with "..." when too long. It is only shown when there is room for a
	// meaningful title and description side by side.
	// Feed text is untrusted: flatten it to one line and drop control
	// characters so it can neither break the row layout nor inject
	// terminal escape sequences.
	title := SanitizeLine(item.Title)
	desc := SanitizeLine(item.Description)

	var titleCol, descCol string
	descLen := 0
	if desc != "" && avail >= minTitle+margin+minDesc {
		titleW := avail * 3 / 5
		descW := avail - titleW - margin // margin separates title and description
		titleCol = truncateEllipsis(title, titleW)
		descCol = descStyle.Render(truncateWithSuffix(desc, descW, "..."))
		descLen = lipgloss.Width(descCol)
	} else {
		titleCol = truncateEllipsis(title, avail)
	}
	titleLen := lipgloss.Width(titleCol)

	// Right-align domain by padding between the content and the domain.
	used := prefixLen + sourceLen + tagsLen + titleLen
	if descLen > 0 {
		used += margin + descLen
	}
	between := t.width - used - domainLen
	if between < 0 {
		between = 0
	}
	padding := strings.Repeat(" ", between)

	// Assemble: viewed date source tags title description <padding> domain
	row := viewedCol + dateCol + " "
	if sourceCol != "" {
		row += sourceCol + " "
	}
	if tagsCol != "" {
		row += tagsCol + " "
	}
	row += titleCol
	if descLen > 0 {
		row += " " + descCol
	}
	row += padding + domainCol

	// Pad or truncate to exact width.
	row = lipgloss.NewStyle().Width(t.width).Render(row)

	// Apply selection highlight.
	if selected {
		row = rowSelected.Render(row)
	}
	return row
}

// renderTags formats tag chips, e.g. "[rust] [go]".
func (t *Table) renderTags(tags []*pb.Tag) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, len(tags))
	for i, tag := range tags {
		parts[i] = t.renderTagChip(tag)
	}
	return strings.Join(parts, " ")
}

// renderScores formats one chip per assessor that scored the item, e.g.
// "[claude 0.9]", in the assessor's color. Names are untrusted text.
func (t *Table) renderScores(item *pb.Item) string {
	chips := scoreChips(item, t.selectedAssessor)
	if len(chips) == 0 {
		return ""
	}
	parts := make([]string, len(chips))
	for i, c := range chips {
		name := c.name
		color := ""
		if a, ok := t.assessors[c.assessorID]; ok {
			name, color = a.Name, a.Color
		}
		label := SanitizeLine(name) + " " + formatScore(c.score)
		if color != "" {
			label = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(label)
		}
		parts[i] = tagBracketStyle.Render("[") + label + tagBracketStyle.Render("]")
	}
	return strings.Join(parts, " ")
}

// renderTagChip renders a single tag with its color (if known).
func (t *Table) renderTagChip(tag *pb.Tag) string {
	color := tag.Color
	if color == "" {
		if c, ok := t.tagColors[tag.Name]; ok {
			color = c
		}
	}

	lb := tagBracketStyle.Render("[")
	rb := tagBracketStyle.Render("]")

	name := tag.Name
	if color != "" {
		name = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(name)
	}
	return lb + name + rb
}

// renderSourceChip renders a source chip using its source_id to look up display
// metadata in the sourceMeta map. Returns empty string when no metadata is found.
func (t *Table) renderSourceChip(sourceID int64) string {
	meta, ok := t.sourceMeta[sourceID]
	if !ok {
		return ""
	}

	lb := tagBracketStyle.Render("[")
	rb := tagBracketStyle.Render("]")

	name := meta.Name
	if name == "" {
		return ""
	}
	if meta.Color != "" {
		name = lipgloss.NewStyle().Foreground(lipgloss.Color(meta.Color)).Render(name)
	}
	return lb + name + rb
}

// ── Helpers ────────────────────────────────────────────────────

// formatDate renders a published timestamp as a fixed-width "dd.MM HH:mm" string.
// It returns dateColumnWidth spaces when the timestamp is missing, keeping
// columns aligned. The time is formatted as-is (UTC) for deterministic output.
func formatDate(published *timestamppb.Timestamp) string {
	if published == nil {
		return strings.Repeat(" ", dateColumnWidth)
	}
	return published.AsTime().Format("02.01 15:04")
}

// extractDomain extracts the host from a URL string for display.
func extractDomain(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	// Strip www. prefix for compactness.
	host = strings.TrimPrefix(host, "www.")
	return host
}

// SanitizeLine prepares untrusted text for single-line terminal output: runs
// of whitespace (including newlines and tabs) collapse into one space and all
// other control characters, such as ESC and the C1 range, are removed.
func SanitizeLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// truncateEllipsis truncates s to at most maxWidth rune-cells, appending "..".
func truncateEllipsis(s string, maxWidth int) string {
	return truncateWithSuffix(s, maxWidth, "..")
}

// truncateWithSuffix truncates s to at most maxWidth display cells, appending
// suffix when s overflows.
func truncateWithSuffix(s string, maxWidth int, suffix string) string {
	if maxWidth <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxWidth {
		return s
	}
	suffixW := lipgloss.Width(suffix)
	if maxWidth <= suffixW {
		return strings.Repeat(".", maxWidth)
	}
	limit := maxWidth - suffixW
	var runes []rune
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > limit {
			break
		}
		runes = append(runes, r)
		w += rw
	}
	return string(runes) + suffix
}
