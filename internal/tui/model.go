// Package tui provides the Nyttig terminal UI components.
//
// model.go implements the root Bubble Tea Model that composes the filter bar,
// table, and status bar. It handles all keybindings, the gRPC stream message
// loop, and debounced view tracking (K9s-style: scroll past = viewed).
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Internal messages ─────────────────────────────────────────

// tickMsg is a message sent by the view-tracking ticker.
type tickMsg time.Time

// sourcesLoadedMsg carries source/tag metadata and tag color map fetched on startup.
type sourcesLoadedMsg struct {
	sources    []SourceInfo
	tags       []TagInfo
	tagColors  map[string]string
	sourceMeta map[int64]sourceDisplay
}

// ── Model ─────────────────────────────────────────────────────

// Model is the root Bubble Tea model for the Nyttig TUI.
type Model struct {
	client *client.Client
	sub    *client.StreamSub

	table  Table
	filter FilterBar
	status StatusBar

	// View tracking: items that have scrolled into view but not yet sent.
	pendingViewed map[int64]bool

	// Stream state.
	batchComplete bool
	quitting      bool

	// Terminal dimensions.
	width  int
	height int

	// Whether the initial filter has been sent (StartStream will load with it).
	streamStarted bool

	// Cached source/tag metadata for the filter dropdowns.
	sources []SourceInfo
	tags    []TagInfo
}

// NewModel creates the root TUI model with a connected gRPC client.
func NewModel(cl *client.Client) Model {
	return Model{
		client:        cl,
		table:         NewTable(),
		filter:        NewFilterBar(),
		status:        NewStatusBar(),
		pendingViewed: make(map[int64]bool),
	}
}

// ── Init ───────────────────────────────────────────────────────

// Init starts the TUI: connect, load metadata, and begin the StreamItems flow.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.connectAndLoad,
	)
}

// connectAndLoad dials the daemon, fetches source/tag metadata to populate
// the filter dropdowns, then starts the StreamItems stream.
func (m *Model) connectAndLoad() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := m.client.Dial(ctx); err != nil {
		return StreamErrorMsg{Err: fmt.Errorf("connect: %w", err)}
	}

	// Load sources for the filter dropdown.
	srcResp, err := m.client.ListSources(ctx)
	if err != nil {
		return StreamErrorMsg{Err: fmt.Errorf("list sources: %w", err)}
	}

	srcs := make([]SourceInfo, 0, len(srcResp.Sources))
	for _, src := range srcResp.Sources {
		srcs = append(srcs, SourceInfo{
			ID:           src.Id,
			Name:         src.Name,
			Color:        src.Color,
			Abbreviation: src.Abbreviation,
		})
	}

	// Load tags for the filter dropdown.
	tagResp, err := m.client.ListTags(ctx)
	if err != nil {
		return StreamErrorMsg{Err: fmt.Errorf("list tags: %w", err)}
	}

	tags := make([]TagInfo, 0, len(tagResp.Tags))
	tagColors := make(map[string]string)
	for _, tag := range tagResp.Tags {
		tags = append(tags, TagInfo{ID: tag.Id, Name: tag.Name, Color: tag.Color, ParentIDs: tag.ParentIds})
		if tag.Color != "" {
			tagColors[tag.Name] = tag.Color
		}
	}

	sourceMeta := make(map[int64]sourceDisplay)
	for _, src := range srcResp.Sources {
		display := src.Name
		if src.Abbreviation != "" {
			display = src.Abbreviation
		}
		sourceMeta[src.Id] = sourceDisplay{Name: display, Color: src.Color}
	}

	return sourcesLoadedMsg{sources: srcs, tags: tags, tagColors: tagColors, sourceMeta: sourceMeta}
}

// startStream sends the initial StreamFilter and begins listening for items.
func (m *Model) startStream() tea.Cmd {
	if m.streamStarted {
		return nil
	}
	m.streamStarted = true

	initialFilter := &pb.StreamFilter{
		SourceId: m.filter.CurrentSourceID(),
		TagId:    m.filter.CurrentTagID(),
		Search:   m.filter.CurrentSearch(),
		Sort:     m.filter.CurrentSort(),
	}

	sub, err := m.client.StreamItems(context.Background(), initialFilter)
	if err != nil {
		return func() tea.Msg { return StreamErrorMsg{Err: fmt.Errorf("start stream: %w", err)} }
	}
	m.sub = sub
	return ListenStream(sub)
}

// startViewTicker returns a command that fires every ~3 seconds for view tracking.
func startViewTicker() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// ── Update ─────────────────────────────────────────────────────

// Update processes incoming messages (keypress, stream data, timers).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Window size ────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.filter.SetWidth(msg.Width)
		// Table height = available rows minus filter (1) and status (1).
		tableHeight := msg.Height - 2
		if tableHeight < 1 {
			tableHeight = 1
		}
		m.table.SetSize(msg.Width, tableHeight)
		return m, nil

	// ── Keypress ────────────────────────────────────────
	case tea.KeyMsg:
		return m.handleKey(msg)

	// ── Stream messages ─────────────────────────────────
	case ItemMsg:
		// The initial batch arrives already sorted by the server. Items pushed
		// live after it are newer than everything shown, so under "newest"
		// they belong at the top.
		if m.batchComplete && m.filter.CurrentSort() == "newest" {
			m.table.PrependItems([]*pb.Item{msg.Item})
		} else {
			m.table.AppendItems([]*pb.Item{msg.Item})
		}
		// Continue reading.
		if m.sub != nil {
			return m, tea.Batch(ListenStream(m.sub), m.trackVisible())
		}
		return m, m.trackVisible()

	case ItemUpdateMsg:
		// Keep reading; the table does not show assessments yet.
		if m.sub != nil {
			return m, ListenStream(m.sub)
		}
		return m, nil

	case ResetMsg:
		m.table.SetItems(nil)
		m.batchComplete = false
		if m.sub != nil {
			return m, ListenStream(m.sub)
		}
		return m, nil

	case CompleteMsg:
		m.batchComplete = true
		if m.sub != nil {
			return m, ListenStream(m.sub)
		}
		return m, nil

	case StreamErrorMsg:
		if msg.Err != nil {
			m.status.SetConnected(false)
		}
		return m, nil

	// ── Filter changes ──────────────────────────────────
	case FilterChangedMsg:
		if m.sub != nil {
			m.sub.SendFilter(&pb.StreamFilter{
				SourceId: msg.SourceID,
				TagId:    msg.TagID,
				Search:   msg.Search,
				Sort:     msg.Sort,
			})
		}
		return m, nil

	// ── Startup metadata ────────────────────────────────
	case sourcesLoadedMsg:
		m.sources = msg.sources
		m.tags = msg.tags
		m.filter.SetSources(msg.sources)
		m.filter.SetTags(msg.tags)
		m.table.SetTagColors(msg.tagColors)
		m.table.SetSourceMeta(msg.sourceMeta)
		m.status.SetConnected(true)
		m.status.SetSourceCount(len(msg.sources))
		return m, tea.Batch(m.startStream(), startViewTicker())

	// ── View ticker ─────────────────────────────────────
	case tickMsg:
		cmds := []tea.Cmd{startViewTicker()}
		if cmds2 := m.flushViewed(); cmds2 != nil {
			cmds = append(cmds, cmds2...)
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// handleKey processes keyboard input, delegating to filter or table actions.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If search is active, handle text input there first.
	if m.filter.IsSearching() {
		switch msg.String() {
		case "esc":
			return m, m.broadcastFilterChange(m.filter.ClearSearch())
		case "enter":
			m.filter.ConfirmSearch()
			return m, nil
		case "backspace":
			return m, m.broadcastFilterChange(m.filter.BackspaceSearch())
		default:
			// Only accept printable single runes.
			if len(msg.Runes) == 1 && msg.Runes[0] >= 32 {
				return m, m.broadcastFilterChange(m.filter.AppendSearchChar(msg.Runes[0]))
			}
			return m, nil
		}
	}

	// Global keybindings.
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		if m.sub != nil {
			// Flush any pending viewed items.
			m.flushViewed()
			m.sub.Close()
		}
		return m, tea.Quit

	case "/":
		m.filter.ToggleSearch()
		return m, nil

	case "s":
		return m, m.broadcastFilterChange(m.filter.CycleSource())

	case "t":
		return m, m.broadcastFilterChange(m.filter.CycleTag())

	case "o":
		return m, m.broadcastFilterChange(m.filter.CycleSort())

	case "r":
		return m, m.refreshAll()

	case "enter":
		return m, m.openLink()

	case "j", "down":
		m.table.MoveDown(1)
		return m, m.trackVisible()

	case "k", "up":
		m.table.MoveUp(1)
		return m, m.trackVisible()

	case "g", "home":
		m.table.GoToTop()
		return m, m.trackVisible()

	case "G", "end":
		m.table.GoToBottom()
		return m, m.trackVisible()

	case "ctrl+d":
		m.table.MoveDown(m.tableHeight())
		return m, m.trackVisible()

	case "ctrl+u":
		m.table.MoveUp(m.tableHeight())
		return m, m.trackVisible()
	}

	return m, nil
}

// tableHeight returns the number of visible rows in the table.
func (m *Model) tableHeight() int {
	th := m.height - 2
	if th < 1 {
		return 1
	}
	return th
}

// broadcastFilterChange takes a FilterChangedMsg and broadcasts it
// to both the gRPC stream (for server-side filtering) and the model's Update.
func (m *Model) broadcastFilterChange(fc FilterChangedMsg) tea.Cmd {
	return func() tea.Msg { return fc }
}

// refreshAll sends a RefreshSource(0) RPC to the daemon.
func (m *Model) refreshAll() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.client.RefreshSource(ctx, 0); err != nil {
			// Log but don't disrupt the UI.
			_ = err
		}
		return nil
	}
}

// openLink opens the selected item's link in the default browser.
func (m *Model) openLink() tea.Cmd {
	item := m.table.SelectedItem()
	if item == nil || item.Link == "" {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command("xdg-open", item.Link).Start()
		return nil
	}
}

// ── View tracking ─────────────────────────────────────────────

// trackVisible adds the currently visible item IDs to the pending set.
func (m *Model) trackVisible() tea.Cmd {
	ids := m.table.VisibleItemIDs()
	for _, id := range ids {
		m.pendingViewed[id] = true
	}
	return nil
}

// flushViewed sends the accumulated pending viewed items to the daemon.
func (m *Model) flushViewed() []tea.Cmd {
	if len(m.pendingViewed) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(m.pendingViewed))
	for id := range m.pendingViewed {
		ids = append(ids, id)
	}
	// Clear the pending set.
	m.pendingViewed = make(map[int64]bool)

	// Mark as viewed in a goroutine; update local items too.
	for _, id := range ids {
		for i := range m.table.items {
			if m.table.items[i].Id == id {
				m.table.items[i].Viewed = true
				break
			}
		}
	}

	return []tea.Cmd{
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = m.client.MarkViewed(ctx, ids)
			return nil
		},
	}
}

// ── View ───────────────────────────────────────────────────────

// View renders the complete TUI: filter bar, table, and status bar.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	filterView := m.filter.View()
	tableView := m.table.View()
	statusView := m.status.View(m.width)

	return filterView + "\n" + tableView + "\n" + statusView
}
