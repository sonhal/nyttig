// Package tui provides the Nyttig terminal UI components.
//
// table.go implements a custom Lipgloss table renderer for the news item list.
// It renders a scrollable viewport with unviewed indicators, colored tag chips,
// truncated titles, right-aligned domains, and row highlighting.
package tui

import (
	"net/url"
	"strings"

	"github.com/charmbracelet/lipgloss"

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

	domainStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#569CD6"))

	tagBracketStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#808080"))
)

// ── Table ──────────────────────────────────────────────────────

// Table renders a scrollable viewport of news items using custom Lipgloss styles.
type Table struct {
	items  []*pb.Item
	cursor int // selected row index (0-based)
	offset int // first visible row index (scroll position)

	width  int
	height int // number of visible rows

	// Tag lookup maps tag name to color (from status bar context).
	tagColors map[string]string
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
	//   viewed (2) | time (5) | tags (variable) | title (fill) | domain (right)

	// 1. Viewed indicator.
	viewedCol := "  "
	if !item.Viewed {
		viewedCol = unviewedDot + " "
	}

	// 2. Time.
	timeCol := "     " // 5 spaces default
	if item.Published != nil {
		tm := item.Published.AsTime()
		timeCol = tm.Format("15:04")
	}
	timeCol = timeStyle.Render(timeCol)

	// 3. Tags.
	tagsCol := t.renderTags(item.Tags)

	// 4. Title + domain.
	// Compute available width for title+domain.
	// viewed(2) + space(1) + time(5) + space(1) + tags(N) + space(1) + ...
	const margin = 1
	prefixLen := 2 + margin + 5 + margin
	tagsLen := lipgloss.Width(tagsCol)
	if tagsLen > 0 {
		tagsLen += margin
	}

	// Domain from link.
	domain := extractDomain(item.Link)
	domainCol := domainStyle.Render(domain)
	domainLen := lipgloss.Width(domainCol)

	titleAvail := t.width - prefixLen - tagsLen - margin - domainLen
	if titleAvail < 5 {
		titleAvail = 5
	}

	title := truncateEllipsis(item.Title, titleAvail)
	titleCol := title

	// Right-align domain by padding between title and domain.
	titleActual := lipgloss.Width(titleCol)
	between := t.width - prefixLen - tagsLen - margin - titleActual - domainLen
	if between < 0 {
		between = 0
	}
	padding := strings.Repeat(" ", between)

	// Assemble.
	row := viewedCol + timeCol + " "
	if tagsCol != "" {
		row += tagsCol + " "
	}
	row += titleCol + padding + domainCol

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

// ── Helpers ────────────────────────────────────────────────────

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

// truncateEllipsis truncates s to at most maxWidth rune-cells, appending "..".
func truncateEllipsis(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	width := lipgloss.Width(s)
	if width <= maxWidth {
		return s
	}
	if maxWidth <= 2 {
		return strings.Repeat(".", maxWidth)
	}
	// Iterate runes to count display width.
	var runes []rune
	w := 0
	suffix := ".."
	suffixW := lipgloss.Width(suffix)
	limit := maxWidth - suffixW
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
