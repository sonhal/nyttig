package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// The digests screen (the D key): the series of every assessor on the left,
// the selected series' history under them, and the digest being read on the
// right, scrollable. It loads when opened and on r; there are no live updates,
// and the feed's stream and filter are not touched while it is open (the
// model keeps handling stream messages, the screen only draws over the feed).
//
// A digest is untrusted text (an LLM wrote it): titles and names go through
// cleanLine, the body through renderMarkdown, which strips control characters
// before anything else (digests_render.go).

// digestAPI is what the screen needs of the client.
type digestAPI interface {
	ListDigestSeries(ctx context.Context, assessorID int64) (*pb.ListDigestSeriesResponse, error)
	ListDigests(ctx context.Context, req *pb.ListDigestsRequest) (*pb.ListDigestsResponse, error)
	GetDigest(ctx context.Context, id int64) (*pb.Digest, error)
}

const (
	digestPageSize   = 20
	digestRPCTimeout = 10 * time.Second
)

var (
	digestSelStyle  = lipgloss.NewStyle().Background(lipgloss.Color("#3A3D41")).Foreground(lipgloss.Color("#FFFFFF"))
	digestHelpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E0E0E0")).Background(lipgloss.Color("#2D2D2D"))
	digestErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F44747")).Background(lipgloss.Color("#2D2D2D"))
	digestDimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
)

// ── Messages ──────────────────────────────────────────────────

type digestSeriesMsg struct {
	series []*pb.DigestSeries
	err    error
}

type digestHistoryMsg struct {
	seriesID int64
	digests  []*pb.Digest
	hasMore  bool
	// older is true for a page after the first one.
	older bool
	err   error
}

type digestMsg struct {
	id     int64
	digest *pb.Digest
	err    error
}

// ── Screen ────────────────────────────────────────────────────

type digestsScreen struct {
	api    digestAPI
	colors map[int64]string // assessor ID -> hex color

	width, height int

	series []*pb.DigestSeries // in list order: grouped by assessor
	cursor int                // index into series

	history    []*pb.Digest // newest period first
	hasMore    bool
	histCursor int // index into history of the digest being read, or -1
	loadingOld bool
	// stepOlder: the user asked for the next older digest while the older page was loading.
	stepOlder bool

	digest *pb.Digest // being read
	want   int64      // the digest a load is in flight for
	scroll int

	bodyKey   string
	bodyLines []string

	loaded bool
	err    string
	// closed is set when the user leaves the screen.
	closed bool
}

func newDigestsScreen(api digestAPI, colors map[int64]string, width, height int) *digestsScreen {
	return &digestsScreen{api: api, colors: colors, width: width, height: height, histCursor: -1}
}

// listOrder groups the series by assessor, in the order of each assessor's
// first series (the list arrives in display order).
func listOrder(series []*pb.DigestSeries) []*pb.DigestSeries {
	var order []int64
	groups := map[int64][]*pb.DigestSeries{}
	for _, s := range series {
		if _, ok := groups[s.AssessorId]; !ok {
			order = append(order, s.AssessorId)
		}
		groups[s.AssessorId] = append(groups[s.AssessorId], s)
	}
	out := make([]*pb.DigestSeries, 0, len(series))
	for _, id := range order {
		out = append(out, groups[id]...)
	}
	return out
}

func (s *digestsScreen) current() *pb.DigestSeries {
	if s.cursor < 0 || s.cursor >= len(s.series) {
		return nil
	}
	return s.series[s.cursor]
}

// ── Commands ──────────────────────────────────────────────────

func (s *digestsScreen) Init() tea.Cmd { return s.loadSeries() }

func (s *digestsScreen) loadSeries() tea.Cmd {
	api := s.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), digestRPCTimeout)
		defer cancel()
		resp, err := api.ListDigestSeries(ctx, 0)
		if err != nil {
			return digestSeriesMsg{err: err}
		}
		return digestSeriesMsg{series: resp.Series}
	}
}

func (s *digestsScreen) loadHistory(seriesID, before int64) tea.Cmd {
	api := s.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), digestRPCTimeout)
		defer cancel()
		resp, err := api.ListDigests(ctx, &pb.ListDigestsRequest{SeriesId: seriesID, BeforeId: before, Limit: digestPageSize})
		if err != nil {
			return digestHistoryMsg{seriesID: seriesID, older: before != 0, err: err}
		}
		return digestHistoryMsg{seriesID: seriesID, digests: resp.Digests, hasMore: resp.HasMore, older: before != 0}
	}
}

func (s *digestsScreen) loadDigest(id int64) tea.Cmd {
	s.want = id
	api := s.api
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), digestRPCTimeout)
		defer cancel()
		d, err := api.GetDigest(ctx, id)
		return digestMsg{id: id, digest: d, err: err}
	}
}

// ── Update ────────────────────────────────────────────────────

// Update handles the screen's messages; messages of other kinds are ignored.
func (s *digestsScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		s.width, s.height = msg.Width, msg.Height
		s.clampScroll()

	case digestSeriesMsg:
		if msg.err != nil {
			s.err = "load series: " + cleanLine(msg.err.Error())
			s.loaded = true
			return nil
		}
		s.err = ""
		s.loaded = true
		var keep int64
		if cur := s.current(); cur != nil {
			keep = cur.Id
		}
		s.series = listOrder(msg.series)
		s.cursor = 0
		for i, sr := range s.series {
			if sr.Id == keep {
				s.cursor = i
			}
		}
		return s.openSeries()

	case digestHistoryMsg:
		cur := s.current()
		if cur == nil || cur.Id != msg.seriesID {
			return nil
		}
		if msg.err != nil {
			s.loadingOld = false
			s.stepOlder = false
			s.err = "load digests: " + cleanLine(msg.err.Error())
			return nil
		}
		s.err = ""
		if msg.older {
			s.loadingOld = false
			s.history = mergeDigests(s.history, msg.digests)
			s.hasMore = msg.hasMore
			if s.stepOlder {
				s.stepOlder = false
				return s.step(1)
			}
			return nil
		}
		s.history, s.hasMore = msg.digests, msg.hasMore
		s.histCursor = -1
		if s.digest != nil && s.digest.SeriesId == cur.Id {
			s.histCursor = indexOfDigest(s.history, s.digest.Id)
			return nil
		}
		s.digest = nil
		if len(s.history) > 0 {
			s.histCursor = 0
			return s.loadDigest(s.history[0].Id)
		}

	case digestMsg:
		if msg.id != s.want {
			return nil
		}
		if msg.err != nil {
			s.err = "load digest: " + cleanLine(msg.err.Error())
			return nil
		}
		s.err = ""
		s.digest = msg.digest
		s.scroll = 0
		s.bodyKey = ""
		// An input from another series: the screen follows it there.
		if cur := s.current(); cur == nil || cur.Id != msg.digest.SeriesId {
			for i, sr := range s.series {
				if sr.Id == msg.digest.SeriesId {
					s.cursor = i
					s.history, s.hasMore, s.histCursor = nil, false, -1
					return s.loadHistory(sr.Id, 0)
				}
			}
			return nil
		}
		s.histCursor = indexOfDigest(s.history, msg.digest.Id)
	}
	return nil
}

func indexOfDigest(list []*pb.Digest, id int64) int {
	for i, d := range list {
		if d.Id == id {
			return i
		}
	}
	return -1
}

// mergeDigests appends a page of older digests, dropping ones already listed.
func mergeDigests(list, page []*pb.Digest) []*pb.Digest {
	seen := make(map[int64]bool, len(list))
	for _, d := range list {
		seen[d.Id] = true
	}
	for _, d := range page {
		if !seen[d.Id] {
			list = append(list, d)
		}
	}
	return list
}

// openSeries (re)loads the history of the selected series.
func (s *digestsScreen) openSeries() tea.Cmd {
	cur := s.current()
	s.history, s.hasMore, s.histCursor, s.loadingOld, s.stepOlder = nil, false, -1, false, false
	s.digest, s.want, s.scroll, s.bodyKey = nil, 0, 0, ""
	if cur == nil {
		return nil
	}
	return s.loadHistory(cur.Id, 0)
}

// step moves to the next older (1) or newer (-1) digest of the series. At the
// end of what is loaded, older digests are fetched first.
func (s *digestsScreen) step(dir int) tea.Cmd {
	if len(s.history) == 0 {
		return nil
	}
	next := s.histCursor + dir
	if s.histCursor < 0 {
		next = 0
	}
	if next < 0 {
		return nil
	}
	if next >= len(s.history) {
		cur := s.current()
		if !s.hasMore || s.loadingOld || cur == nil {
			return nil
		}
		s.loadingOld, s.stepOlder = true, true
		return s.loadHistory(cur.Id, s.history[len(s.history)-1].Id)
	}
	s.histCursor = next
	return s.loadDigest(s.history[next].Id)
}

// ── Keys ──────────────────────────────────────────────────────

// handleKey returns what a key does. The screen is closed by q or Esc, which
// sets closed.
func (s *digestsScreen) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		s.closed = true
	case "j", "down":
		return s.step(1)
	case "k", "up":
		return s.step(-1)
	case "g", "home":
		if len(s.history) > 0 {
			s.histCursor = 0
			return s.loadDigest(s.history[0].Id)
		}
	case "G", "end":
		if n := len(s.history); n > 0 {
			s.histCursor = n - 1
			return s.loadDigest(s.history[n-1].Id)
		}
	case "]", "l", "right":
		return s.moveSeries(1)
	case "[", "h", "left":
		return s.moveSeries(-1)
	case "d", "ctrl+d", "pgdown", " ":
		s.scrollBy(s.bodyHeight() / 2)
	case "u", "ctrl+u", "pgup":
		s.scrollBy(-s.bodyHeight() / 2)
	case "r":
		s.err = ""
		return s.loadSeries()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		n := int(msg.String()[0] - '0')
		if s.digest != nil && n <= len(s.digest.Inputs) {
			return s.loadDigest(s.digest.Inputs[n-1].Id)
		}
	}
	return nil
}

func (s *digestsScreen) moveSeries(dir int) tea.Cmd {
	next := s.cursor + dir
	if next < 0 || next >= len(s.series) {
		return nil
	}
	s.cursor = next
	return s.openSeries()
}

func (s *digestsScreen) bodyHeight() int {
	if h := s.height - 1; h > 1 {
		return h
	}
	return 1
}

func (s *digestsScreen) scrollBy(n int) {
	s.scroll += n
	s.clampScroll()
}

func (s *digestsScreen) clampScroll() {
	max := len(s.content()) - s.bodyHeight()
	if s.scroll > max {
		s.scroll = max
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

// ── View ──────────────────────────────────────────────────────

func (s *digestsScreen) leftWidth() int {
	w := s.width / 3
	if w < 24 {
		w = 24
	}
	if w > 40 {
		w = 40
	}
	if w > s.width-12 {
		w = s.width - 12
	}
	if w < 8 {
		w = 8
	}
	return w
}

func (s *digestsScreen) rightWidth() int {
	w := s.width - s.leftWidth() - 1
	if w < 1 {
		return 1
	}
	return w
}

var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func colorStyle(hex string) lipgloss.Style {
	if hexColorRe.MatchString(hex) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
	}
	return lipgloss.NewStyle()
}

func padTo(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func period(d *pb.Digest) string {
	if d.PeriodStart == nil || d.PeriodEnd == nil {
		return ""
	}
	a, b := d.PeriodStart.AsTime().UTC(), d.PeriodEnd.AsTime().UTC()
	whole := func(t time.Time) bool { return t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 }
	dayEnd := func(t time.Time) bool { return t.Hour() == 23 && t.Minute() == 59 && t.Second() == 59 }
	if whole(a) && (dayEnd(b) || whole(b)) {
		if a.Format("2006-01-02") == b.Format("2006-01-02") {
			return a.Format("2006-01-02")
		}
		return a.Format("2006-01-02") + " .. " + b.Format("2006-01-02")
	}
	return a.Format("2006-01-02 15:04Z") + " .. " + b.Format("2006-01-02 15:04Z")
}

// window picks the first of h rows to show so that row sel is in view.
func window(sel, total, h int) int {
	if total <= h || sel < h/2 {
		return 0
	}
	if sel > total-(h+1)/2 {
		return total - h
	}
	return sel - h/2
}

// seriesPane draws the list of series, grouped by assessor.
func (s *digestsScreen) seriesPane(w, h int) []string {
	type row struct {
		text  string
		style lipgloss.Style
		sel   bool
	}
	var rows []row
	selRow := 0
	last := int64(-1)
	for i, sr := range s.series {
		if sr.AssessorId != last {
			last = sr.AssessorId
			rows = append(rows, row{text: "[" + cleanLine(sr.AssessorName) + "]", style: colorStyle(s.colors[sr.AssessorId])})
		}
		if i == s.cursor {
			selRow = len(rows)
		}
		count := fmt.Sprintf("%d", sr.DigestCount)
		name := truncateEllipsis(cleanLine(sr.Name), w-3-lipgloss.Width(count)-1)
		rows = append(rows, row{text: "  " + padTo(name, w-3-lipgloss.Width(count)) + " " + count, sel: i == s.cursor})
	}
	if len(rows) == 0 {
		msg := "no digest series"
		if !s.loaded {
			msg = "loading…"
		}
		rows = append(rows, row{text: msg, style: digestDimStyle})
	}
	start := window(selRow, len(rows), h)
	var out []string
	for i := start; i < len(rows) && len(out) < h; i++ {
		r := rows[i]
		text := padTo(truncateEllipsis(r.text, w), w)
		if r.sel {
			out = append(out, digestSelStyle.Render(text))
		} else {
			out = append(out, r.style.Render(text))
		}
	}
	return out
}

// historyPane draws the selected series' digests, newest period first.
func (s *digestsScreen) historyPane(w, h int) []string {
	var out []string
	if cur := s.current(); cur != nil && h > 0 {
		out = append(out, padTo(truncateEllipsis(cleanLine(cur.AssessorName)+"/"+cleanLine(cur.Name), w), w))
	}
	room := h - len(out)
	rows := make([]string, 0, len(s.history)+1)
	for _, d := range s.history {
		p := period(d)
		rows = append(rows, truncateEllipsis(p+"  "+cleanLine(d.Title), w))
	}
	if s.hasMore {
		rows = append(rows, "… more with j")
	}
	start := window(s.histCursor, len(rows), room)
	for i := start; i < len(rows) && len(out) < h; i++ {
		text := padTo(rows[i], w)
		if i == s.histCursor {
			out = append(out, digestSelStyle.Render(text))
		} else if i == len(s.history) {
			out = append(out, digestDimStyle.Render(text))
		} else {
			out = append(out, text)
		}
	}
	return out
}

// content is the whole of the reading pane: the digest's header, its body,
// the items it is based on and its inputs, as lines.
func (s *digestsScreen) content() []string {
	d := s.digest
	if d == nil {
		return nil
	}
	w := s.rightWidth()
	key := fmt.Sprintf("%d/%d/%d", d.Id, d.UpdatedAt.AsTime().UnixNano(), w)
	if key == s.bodyKey {
		return s.bodyLines
	}
	var out []string
	out = append(out, mdHead.Render(truncateEllipsis(cleanLine(d.Title), w)))
	meta := cleanLine(d.AssessorName) + "/" + cleanLine(d.SeriesName) + " · " + period(d)
	out = append(out, digestDimStyle.Render(truncateEllipsis(meta, w)), "")
	out = append(out, renderMarkdown(d.Body, d.Items, w)...)
	if len(d.Items) > 0 {
		out = append(out, "", digestDimStyle.Render(fmt.Sprintf("Based on %d items:", len(d.Items))))
		for _, it := range d.Items {
			line := fmt.Sprintf("[#%d] %s (%s)", it.ItemId, cleanLine(it.Title), cleanLine(it.SourceName))
			out = append(out, wrapSpans([]span{{line, spText}}, "  ", "      ", w)...)
			if u := safeURL(it.Link); u != "" {
				out = append(out, "      "+mdLink.Render(truncateEllipsis(cleanLine(u), w-6)))
			}
		}
	}
	if len(d.Inputs) > 0 {
		out = append(out, "", digestDimStyle.Render(fmt.Sprintf("Inputs (%d digests, 1-9 opens one):", len(d.Inputs))))
		for i, in := range d.Inputs {
			end := ""
			if in.PeriodEnd != nil {
				end = in.PeriodEnd.AsTime().UTC().Format("2006-01-02")
			}
			line := fmt.Sprintf("%d. %s (%s, %s)", i+1, cleanLine(in.Title), cleanLine(in.SeriesName), end)
			out = append(out, wrapSpans([]span{{line, spText}}, "  ", "     ", w)...)
		}
	}
	s.bodyKey, s.bodyLines = key, out
	return out
}

// View draws the screen over the whole window.
func (s *digestsScreen) View() string {
	h := s.bodyHeight()
	lw := s.leftWidth()

	seriesH := h / 2
	left := s.seriesPane(lw, seriesH)
	for len(left) < seriesH {
		left = append(left, strings.Repeat(" ", lw))
	}
	left = append(left, s.historyPane(lw, h-seriesH)...)
	for len(left) < h {
		left = append(left, strings.Repeat(" ", lw))
	}

	content := s.content()
	var right []string
	switch {
	case s.digest == nil && s.err == "" && (!s.loaded || s.current() != nil):
		right = []string{digestDimStyle.Render("loading…")}
	case s.digest == nil:
		right = []string{digestDimStyle.Render("no digest to show")}
	default:
		end := s.scroll + h
		if end > len(content) {
			end = len(content)
		}
		if s.scroll < end {
			right = content[s.scroll:end]
		}
	}

	var b strings.Builder
	sep := digestDimStyle.Render("│")
	for i := 0; i < h; i++ {
		r := ""
		if i < len(right) {
			r = right[i]
		}
		b.WriteString(padTo(left[i], lw) + sep + r + "\n")
	}
	b.WriteString(s.statusLine())
	return b.String()
}

func (s *digestsScreen) statusLine() string {
	if s.err != "" {
		return digestErrStyle.Render(padTo(truncateEllipsis(s.err, s.width), s.width))
	}
	help := "j/k older/newer  [/] series  d/u scroll  1-9 input  r reload  q back"
	return digestHelpStyle.Render(padTo(truncateEllipsis(help, s.width), s.width))
}
