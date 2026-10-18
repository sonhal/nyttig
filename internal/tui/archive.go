package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Archiving ──────────────────────────────────────────────────

var (
	archiveSavedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6A9955"))
	archiveBusyStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	archiveFailedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F44747"))
	archivePartialStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
)

// archiveChip is the row marker of an item's archive: ⤓ saved, … pending or
// running, ✗ failed. A partial copy is saved with a dim · after it. "" when
// the item has no archive.
func archiveChip(a *pb.ArchiveInfo) string {
	if a == nil {
		return ""
	}
	switch a.Status {
	case "done":
		return archiveSavedStyle.Render("⤓")
	case "partial":
		return archiveSavedStyle.Render("⤓") + archivePartialStyle.Render("·")
	case "failed":
		return archiveFailedStyle.Render("✗")
	}
	return archiveBusyStyle.Render("…")
}

// archiveBusy reports whether the daemon is still working on an archive.
func archiveBusy(a *pb.ArchiveInfo) bool {
	return a != nil && (a.Status == "pending" || a.Status == "running")
}

// archiveDoneMsg is the result of asking for an archive.
type archiveDoneMsg struct {
	err error
}

// archiveDetailMsg carries an archive's metadata, fetched for the detail line.
type archiveDetailMsg struct {
	archive *pb.Archive
	err     error
}

// archiveItem queues the archive of the selected item. force archives one
// that is already saved again.
func (m *Model) archiveItem(force bool) (tea.Model, tea.Cmd) {
	item := m.table.SelectedItem()
	switch {
	case item == nil:
		m.note = "no item selected"
	case item.Link == "":
		m.note = "item has no link to archive"
	case archiveBusy(item.Archive):
		m.note = "already being archived"
	case item.Archive != nil && item.Archive.Status != "failed" && !force:
		m.note = "already archived; B archives it again"
	default:
		id, fn := item.Id, m.archive
		m.note = "archiving…"
		m.layout()
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := fn(ctx, id)
			return archiveDoneMsg{err: err}
		}
	}
	m.layout()
	return m, nil
}

// fetchArchiveDetail asks for the error or note of the selected item's
// archive when the detail line is open and the archive has finished. Each
// status of an archive is fetched once.
func (m *Model) fetchArchiveDetail() tea.Cmd {
	if !m.showInfo || m.client == nil {
		return nil
	}
	item := m.table.SelectedItem()
	if item == nil || item.Archive == nil || archiveBusy(item.Archive) {
		return nil
	}
	if cur := m.archiveDetails[item.Archive.Id]; cur != nil && cur.Status == item.Archive.Status {
		return nil
	}
	id, cl := item.Archive.Id, m.client
	m.archiveDetails[id] = &pb.Archive{Id: id, Status: item.Archive.Status} // not asked again meanwhile
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a, err := cl.GetArchive(ctx, id)
		return archiveDetailMsg{archive: a, err: err}
	}
}

// archiveLine is the selected item's archive on the detail line: its error
// or note, "" when there is none.
func (m *Model) archiveLine(item *pb.Item) string {
	if item == nil || item.Archive == nil {
		return ""
	}
	a := m.archiveDetails[item.Archive.Id]
	if a == nil || a.Status != item.Archive.Status {
		return ""
	}
	switch {
	case a.Error != "":
		return "archive " + a.Status + ": " + SanitizeLine(a.Error)
	case a.Note != "":
		return "archive: " + SanitizeLine(a.Note)
	}
	return ""
}
