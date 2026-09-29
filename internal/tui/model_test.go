package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func itemIDs(items []*pb.Item) []int64 {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.Id
	}
	return ids
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// asModel unwraps a tea.Model returned by Update; handlers return either a
// Model or a *Model.
func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	switch v := tm.(type) {
	case Model:
		return v
	case *Model:
		return *v
	}
	t.Fatalf("unexpected model type %T", tm)
	return Model{}
}

func TestModel_SortKeyCyclesSort(t *testing.T) {
	m := NewModel(nil)

	for _, want := range []string{"oldest", "newest"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
		m = asModel(t, next)
		if cmd == nil {
			t.Fatalf("expected a command after pressing 'o'")
		}
		fc, ok := cmd().(FilterChangedMsg)
		if !ok {
			t.Fatalf("expected FilterChangedMsg, got %T", cmd())
		}
		if fc.Sort != want {
			t.Errorf("Sort = %q, want %q", fc.Sort, want)
		}
		if got := m.filter.CurrentSort(); got != want {
			t.Errorf("CurrentSort() = %q, want %q", got, want)
		}
	}
}

func TestModel_LiveItemPlacementFollowsSort(t *testing.T) {
	tests := []struct {
		name          string
		sort          string
		batchComplete bool
		want          []int64
	}{
		{"initial batch newest appends", "newest", false, []int64{1, 2, 3}},
		{"live push newest prepends", "newest", true, []int64{3, 1, 2}},
		{"live push oldest appends", "oldest", true, []int64{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(nil)
			if tt.sort == "oldest" {
				m.filter.CycleSort()
			}
			m.table.SetItems([]*pb.Item{{Id: 1}, {Id: 2}})
			m.batchComplete = tt.batchComplete

			next, _ := m.Update(ItemMsg{Item: &pb.Item{Id: 3}})
			m = asModel(t, next)

			if got := itemIDs(m.table.GetItems()); !equalIDs(got, tt.want) {
				t.Errorf("items = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTable_PrependItems(t *testing.T) {
	t.Run("at top stays at top", func(t *testing.T) {
		tb := newTestTable(80)
		tb.SetItems([]*pb.Item{{Id: 1}, {Id: 2}})
		tb.PrependItems([]*pb.Item{{Id: 3}})
		if got := itemIDs(tb.GetItems()); !equalIDs(got, []int64{3, 1, 2}) {
			t.Errorf("items = %v", got)
		}
		if sel := tb.SelectedItem(); sel.Id != 3 {
			t.Errorf("selected = %d, want 3", sel.Id)
		}
	})

	t.Run("scrolled keeps selection", func(t *testing.T) {
		tb := newTestTable(80)
		tb.SetItems([]*pb.Item{{Id: 1}, {Id: 2}, {Id: 3}})
		tb.MoveDown(1)
		tb.PrependItems([]*pb.Item{{Id: 4}, {Id: 5}})
		if sel := tb.SelectedItem(); sel.Id != 2 {
			t.Errorf("selected = %d, want 2", sel.Id)
		}
	})

	t.Run("empty is a no-op", func(t *testing.T) {
		tb := newTestTable(80)
		tb.SetItems([]*pb.Item{{Id: 1}})
		tb.PrependItems(nil)
		if got := itemIDs(tb.GetItems()); !equalIDs(got, []int64{1}) {
			t.Errorf("items = %v", got)
		}
	})
}
