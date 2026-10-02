package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// ── Stream event message types ───────────────────────────────
// These are Bubble Tea messages that flow from the gRPC StreamItems
// channel into the TUI's Update loop.

// ItemMsg carries a news item pushed by the daemon (initial batch or live push).
type ItemMsg struct {
	Item *pb.Item
}

// ItemUpdateMsg carries an item whose assessments changed, with all of them.
// Matches says whether it matches the stream's current filter.
type ItemUpdateMsg struct {
	Item    *pb.Item
	Matches bool
}

// ResetMsg signals that the server has reset the stream (e.g. after a filter
// change). The TUI should clear the current item list and prepare for a fresh
// batch.
type ResetMsg struct{}

// CompleteMsg signals that the initial batch (or post-reset batch) has been
// fully sent. No more items are coming until a new item arrives or a filter
// change triggers a reset.
type CompleteMsg struct{}

// StreamErrorMsg signals that the gRPC stream terminated with an error.
// Err is nil when the stream closed cleanly (graceful shutdown).
type StreamErrorMsg struct {
	Err error
}

// ── Stream listener ───────────────────────────────────────────

// ListenStream returns a tea.Cmd that reads the next message from the gRPC
// StreamSub and converts it into a Bubble Tea message.
//
// Usage pattern in the Model's Update method:
//
//	case ItemMsg:
//	    m.items = append(m.items, msg.Item)
//	    return m, ListenStream(m.sub)   // keep reading
//	case ResetMsg:
//	    m.items = nil
//	    return m, ListenStream(m.sub)
//	case CompleteMsg:
//	    m.batchComplete = true
//	    return m, ListenStream(m.sub)   // keep reading for live pushes
//	case StreamErrorMsg:
//	    // handle disconnection
//	    return m, nil
func ListenStream(sub *client.StreamSub) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-sub.Messages
		if !ok {
			err := sub.Err()
			return StreamErrorMsg{Err: err}
		}
		switch msg.Msg.(type) {
		case *pb.ServerMessage_Item:
			return ItemMsg{Item: msg.GetItem()}
		case *pb.ServerMessage_ItemUpdate:
			return ItemUpdateMsg{Item: msg.GetItemUpdate(), Matches: msg.GetUpdateMatches()}
		case *pb.ServerMessage_Reset_:
			return ResetMsg{}
		case *pb.ServerMessage_Complete:
			return CompleteMsg{}
		}
		return nil
	}
}
