package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Status bar styles
var (
	statusBarStyle = lipgloss.NewStyle().
			Width(120).
			MaxWidth(120).
			Padding(0, 1).
			Foreground(lipgloss.Color("#E0E0E0")).
			Background(lipgloss.Color("#2D2D2D"))

	statusConnected = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4EC9B0"))

	statusDisconnected = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F44747"))
)

// StatusBar holds the state for the status bar component.
type StatusBar struct {
	Connected   bool
	Unviewed    int
	LastFetch   *time.Time
	NextFetch   *time.Time
	Version     string
	SourceCount int
}

// NewStatusBar creates a new StatusBar with default state.
func NewStatusBar() StatusBar {
	return StatusBar{
		Version: "0.1.0",
	}
}

// View renders the status bar as a styled string.
func (s StatusBar) View(width int) string {
	// Connection indicator.
	connStr := "disconnected"
	connStyle := statusDisconnected
	if s.Connected {
		connStr = "connected"
		connStyle = statusConnected
	}
	connIndicator := connStyle.Render("● " + connStr)

	// Unviewed count.
	unviewedPart := fmt.Sprintf("Unviewed: %d", s.Unviewed)

	// Fetch times.
	lastFetchPart := "Last fetch: --"
	if s.LastFetch != nil {
		ago := time.Since(*s.LastFetch).Round(time.Second)
		if ago < time.Minute {
			lastFetchPart = fmt.Sprintf("Last fetch: %ds ago", int(ago.Seconds()))
		} else if ago < time.Hour {
			lastFetchPart = fmt.Sprintf("Last fetch: %dm ago", int(ago.Minutes()))
		} else {
			lastFetchPart = fmt.Sprintf("Last fetch: %dh ago", int(ago.Hours()))
		}
	}

	nextFetchPart := "Next: --"
	if s.NextFetch != nil {
		in := time.Until(*s.NextFetch).Round(time.Second)
		if in > 0 {
			if in < time.Minute {
				nextFetchPart = fmt.Sprintf("Next: %ds", int(in.Seconds()))
			} else if in < time.Hour {
				nextFetchPart = fmt.Sprintf("Next: %dm", int(in.Minutes()))
			} else {
				nextFetchPart = fmt.Sprintf("Next: %dh", int(in.Hours()))
			}
		} else {
			nextFetchPart = "Next: now"
		}
	}

	// Source count.
	sourcePart := fmt.Sprintf("%d sources", s.SourceCount)

	// Version.
	versionPart := "nyttig " + s.Version

	// Assemble: left side (connection, unviewed, fetch times, sources),
	// right side (version).
	leftParts := []string{connIndicator, unviewedPart, lastFetchPart, nextFetchPart, sourcePart}
	left := strings.Join(leftParts, "   ")

	// Ensure the bar fills the available width.
	// Format: left-aligned segments, right-aligned version.
	padding := width - lipgloss.Width(left) - lipgloss.Width(versionPart) - 2 // 2 = padding
	if padding < 1 {
		padding = 1
	}

	rendered := left + strings.Repeat(" ", padding) + versionPart
	return statusBarStyle.Width(width).Render(rendered)
}

// SetConnected updates the connection state.
func (s *StatusBar) SetConnected(connected bool) {
	s.Connected = connected
}

// SetUnviewed updates the unviewed count.
func (s *StatusBar) SetUnviewed(count int) {
	s.Unviewed = count
}

// SetLastFetch updates the last fetch time.
func (s *StatusBar) SetLastFetch(t time.Time) {
	s.LastFetch = &t
}

// SetNextFetch updates the next expected fetch time.
func (s *StatusBar) SetNextFetch(t time.Time) {
	s.NextFetch = &t
}

// SetSourceCount updates the source count.
func (s *StatusBar) SetSourceCount(n int) {
	s.SourceCount = n
}
