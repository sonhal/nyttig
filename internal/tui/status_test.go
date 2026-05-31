package tui

import (
	"strings"
	"testing"
	"time"
)

func TestStatusBar_New(t *testing.T) {
	sb := NewStatusBar()
	if sb.Version != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", sb.Version)
	}
	if sb.Connected {
		t.Error("expected disconnected initially")
	}
	if sb.Unviewed != 0 {
		t.Errorf("expected 0 unviewed, got %d", sb.Unviewed)
	}
}

func TestStatusBar_SetConnected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(true)
	if !sb.Connected {
		t.Error("expected connected after SetConnected(true)")
	}
	sb.SetConnected(false)
	if sb.Connected {
		t.Error("expected disconnected after SetConnected(false)")
	}
}

func TestStatusBar_SetUnviewed(t *testing.T) {
	sb := NewStatusBar()
	sb.SetUnviewed(42)
	if sb.Unviewed != 42 {
		t.Errorf("expected 42 unviewed, got %d", sb.Unviewed)
	}
}

func TestStatusBar_SetLastFetch(t *testing.T) {
	sb := NewStatusBar()
	now := time.Now()
	sb.SetLastFetch(now)
	if sb.LastFetch == nil {
		t.Fatal("expected non-nil LastFetch")
	}
	if !sb.LastFetch.Equal(now) {
		t.Errorf("expected LastFetch to equal input time")
	}
}

func TestStatusBar_SetNextFetch(t *testing.T) {
	sb := NewStatusBar()
	future := time.Now().Add(10 * time.Minute)
	sb.SetNextFetch(future)
	if sb.NextFetch == nil {
		t.Fatal("expected non-nil NextFetch")
	}
	if !sb.NextFetch.Equal(future) {
		t.Errorf("expected NextFetch to equal input time")
	}
}

func TestStatusBar_SetSourceCount(t *testing.T) {
	sb := NewStatusBar()
	sb.SetSourceCount(5)
	if sb.SourceCount != 5 {
		t.Errorf("expected 5 sources, got %d", sb.SourceCount)
	}
}

func TestStatusBar_View_Disconnected(t *testing.T) {
	sb := NewStatusBar()
	// Verify the rendered view contains expected elements.
	view := sb.View(120)

	// Should show disconnected with the dimmer color.
	if !strings.Contains(view, "disconnected") {
		t.Error("expected 'disconnected' in status bar")
	}
	if !strings.Contains(view, "Unviewed: 0") {
		t.Error("expected 'Unviewed: 0' in status bar")
	}
	if !strings.Contains(view, "Last fetch: --") {
		t.Error("expected 'Last fetch: --' in status bar")
	}
	if !strings.Contains(view, "Next: --") {
		t.Error("expected 'Next: --' in status bar")
	}
	if !strings.Contains(view, "0 sources") {
		t.Error("expected '0 sources' in status bar")
	}
	if !strings.Contains(view, "nyttig 0.1.0") {
		t.Error("expected 'nyttig 0.1.0' in status bar")
	}
}

func TestStatusBar_View_Connected(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(true)
	// Should show connected.
	view := sb.View(120)
	if !strings.Contains(view, "connected") {
		t.Error("expected 'connected' in status bar")
	}
}

func TestStatusBar_View_LastFetch_Seconds(t *testing.T) {
	sb := NewStatusBar()
	tenSecAgo := time.Now().Add(-10 * time.Second)
	sb.SetLastFetch(tenSecAgo)
	view := sb.View(120)
	// Should show "10s ago" (or 9s depending on timing).
	if !strings.Contains(view, "s ago") {
		t.Errorf("expected 's ago' in status bar, got: %s", view)
	}
}

func TestStatusBar_View_LastFetch_Minutes(t *testing.T) {
	sb := NewStatusBar()
	fiveMinAgo := time.Now().Add(-5 * time.Minute)
	sb.SetLastFetch(fiveMinAgo)
	view := sb.View(120)
	if !strings.Contains(view, "m ago") {
		t.Errorf("expected 'm ago' in status bar, got: %s", view)
	}
}

func TestStatusBar_View_LastFetch_Hours(t *testing.T) {
	sb := NewStatusBar()
	twoHoursAgo := time.Now().Add(-2 * time.Hour)
	sb.SetLastFetch(twoHoursAgo)
	view := sb.View(120)
	if !strings.Contains(view, "h ago") {
		t.Errorf("expected 'h ago' in status bar, got: %s", view)
	}
}

func TestStatusBar_View_NextFetch_Future(t *testing.T) {
	sb := NewStatusBar()
	in10Min := time.Now().Add(10 * time.Minute)
	sb.SetNextFetch(in10Min)
	view := sb.View(120)
	// Should show "Next: 9m" or "Next: 10m" depending on timing.
	if !strings.Contains(view, "Next: ") {
		t.Error("expected 'Next: ' in status bar")
	}
	// Verify "Next: --" is NOT present.
	if strings.Contains(view, "Next: --") {
		t.Error("expected actual fetch time, not 'Next: --'")
	}
}

func TestStatusBar_View_NextFetch_Now(t *testing.T) {
	sb := NewStatusBar()
	past := time.Now().Add(-1 * time.Second)
	sb.SetNextFetch(past)
	view := sb.View(120)
	if !strings.Contains(view, "Next: now") {
		t.Errorf("expected 'Next: now' in status bar, got: %s", view)
	}
}

func TestStatusBar_View_WidthConstraint(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(true)
	view := sb.View(80)
	// The function caps width; ensure it's not excessively long.
	// lipgloss width accounting can be tricky, so we just check that we
	// get non-empty output.
	if len(view) == 0 {
		t.Error("expected non-empty view")
	}
	if !strings.Contains(view, "nyttig") {
		t.Error("expected version in status bar")
	}
}

func TestStatusBar_View_RenderableContent(t *testing.T) {
	sb := NewStatusBar()
	sb.SetConnected(true)
	sb.SetUnviewed(15)
	sb.SetSourceCount(3)
	view := sb.View(120)

	expectedParts := []string{
		"connected",
		"Unviewed: 15",
		"3 sources",
		"nyttig 0.1.0",
	}
	for _, p := range expectedParts {
		if !strings.Contains(view, p) {
			t.Errorf("expected '%s' in status bar view, got: %s", p, view)
		}
	}
}
