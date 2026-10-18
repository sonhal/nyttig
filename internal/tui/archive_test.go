package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func archived(id int64, status string) *pb.Item {
	return &pb.Item{Id: id, Title: "item", Link: "https://example.com/a", Archive: &pb.ArchiveInfo{Id: id * 10, Status: status}}
}

func TestArchiveChip(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   string
	}{{"done", "⤓"}, {"partial", "⤓·"}, {"failed", "✗"}, {"pending", "…"}, {"running", "…"}} {
		if got := stripANSI(archiveChip(&pb.ArchiveInfo{Status: tc.status})); got != tc.want {
			t.Errorf("chip(%s) = %q, want %q", tc.status, got, tc.want)
		}
	}
	if archiveChip(nil) != "" {
		t.Error("an item without an archive has a chip")
	}
}

func TestArchiveChip_InRowAndUpdate(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	m.table.SetItems([]*pb.Item{archived(1, "pending")})
	if v := stripANSI(m.View()); !strings.Contains(v, "…") {
		t.Errorf("pending chip missing: %q", v)
	}
	// The daemon's update on finish changes the chip in place.
	next, _ = m.Update(ItemUpdateMsg{Item: archived(1, "done"), Matches: true})
	m = asModel(t, next)
	if v := stripANSI(m.View()); !strings.Contains(v, "⤓") || strings.Contains(v, "…") {
		t.Errorf("done chip missing: %q", v)
	}
	// A removed archive arrives as an item without one.
	next, _ = m.Update(ItemUpdateMsg{Item: &pb.Item{Id: 1, Title: "item"}, Matches: true})
	m = asModel(t, next)
	if v := stripANSI(m.View()); strings.Contains(v, "⤓") {
		t.Errorf("chip stayed after remove: %q", v)
	}
}

func TestModel_ArchiveKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		item  *pb.Item
		key   rune
		calls int
		note  string
	}{
		{"new item", &pb.Item{Id: 1, Link: "https://example.com"}, 'b', 1, "archiving"},
		{"retry a failed one", archived(1, "failed"), 'b', 1, "archiving"},
		{"already saved", archived(1, "done"), 'b', 0, "already archived; B"},
		{"B archives a saved one again", archived(1, "done"), 'B', 1, "archiving"},
		{"B archives a partial one again", archived(1, "partial"), 'B', 1, "archiving"},
		{"queued", archived(1, "pending"), 'B', 0, "already being archived"},
		{"running", archived(1, "running"), 'b', 0, "already being archived"},
		{"no link", &pb.Item{Id: 1}, 'b', 0, "no link"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(nil)
			var asked []int64
			m.archive = func(_ context.Context, id int64) (*pb.Archive, error) {
				asked = append(asked, id)
				return &pb.Archive{Id: 5, Status: "pending"}, nil
			}
			next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
			m = asModel(t, next)
			m.table.SetItems([]*pb.Item{tc.item})
			next, cmd := m.Update(key(tc.key))
			m = asModel(t, next)
			if (cmd != nil) != (tc.calls > 0) {
				t.Fatalf("cmd = %v, want a command: %v", cmd != nil, tc.calls > 0)
			}
			if cmd != nil {
				done, ok := cmd().(archiveDoneMsg)
				if !ok || done.err != nil || len(asked) != tc.calls || asked[0] != 1 {
					t.Fatalf("result %+v, asked %v", done, asked)
				}
			}
			if !strings.Contains(m.note, tc.note) {
				t.Errorf("note = %q, want it to contain %q", m.note, tc.note)
			}
		})
	}
}

func TestModel_ArchiveResultNote(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	next, _ = m.Update(archiveDoneMsg{})
	if m = asModel(t, next); m.note != "archive queued" {
		t.Errorf("note = %q", m.note)
	}
	next, _ = m.Update(archiveDoneMsg{err: errors.New("no link\x1b[2J")})
	m = asModel(t, next)
	if m.note != "archive failed: no link[2J" || strings.Contains(m.View(), "\x1b[2J") {
		t.Errorf("note = %q", m.note)
	}
}

func TestModel_ArchiveDetailLine(t *testing.T) {
	m := NewModel(nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = asModel(t, next)
	m.table.SetItems([]*pb.Item{archived(1, "failed")})
	m.showInfo = true
	m.layout()

	next, _ = m.Update(archiveDetailMsg{archive: &pb.Archive{Id: 10, Status: "failed", Error: "HTTP 403\n\x1b[2J"}})
	m = asModel(t, next)
	v := m.View()
	if !strings.Contains(stripANSI(v), "archive failed: HTTP 403 [2J") || strings.Contains(v, "\x1b[2J") {
		t.Errorf("error not shown as text: %q", stripANSI(v))
	}

	// A note on a saved copy shows too; stale details (older status) do not.
	m.table.SetItems([]*pb.Item{archived(1, "partial")})
	if got := m.archiveLine(m.table.SelectedItem()); got != "" {
		t.Errorf("stale detail shown: %q", got)
	}
	next, _ = m.Update(archiveDetailMsg{archive: &pb.Archive{Id: 10, Status: "partial", Note: "little text"}})
	m = asModel(t, next)
	if got := m.archiveLine(m.table.SelectedItem()); got != "archive: little text" {
		t.Errorf("note line = %q", got)
	}
}
