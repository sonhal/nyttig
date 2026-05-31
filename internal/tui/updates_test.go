package tui

import (
	"errors"
	"testing"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// TestMessageTypes verifies the Bubble Tea message type definitions.
func TestMessageTypes_ItemMsg(t *testing.T) {
	item := &pb.Item{Id: 42, Title: "Hello World"}
	msg := ItemMsg{Item: item}
	if msg.Item.Id != 42 {
		t.Errorf("expected item ID 42, got %d", msg.Item.Id)
	}
	if msg.Item.Title != "Hello World" {
		t.Errorf("expected title 'Hello World', got %s", msg.Item.Title)
	}
}

func TestMessageTypes_ResetMsg(t *testing.T) {
	msg := ResetMsg{}
	_ = msg
}

func TestMessageTypes_CompleteMsg(t *testing.T) {
	msg := CompleteMsg{}
	_ = msg
}

func TestMessageTypes_StreamErrorMsg(t *testing.T) {
	err := errors.New("connection refused")
	msg := StreamErrorMsg{Err: err}
	if msg.Err == nil {
		t.Fatal("expected non-nil error")
	}
	if msg.Err.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %v", msg.Err)
	}
}

func TestMessageTypes_StreamErrorMsg_Nil(t *testing.T) {
	msg := StreamErrorMsg{Err: nil}
	if msg.Err != nil {
		t.Errorf("expected nil error, got %v", msg.Err)
	}
}

// TestListenStream_Item verifies that an Item server message becomes ItemMsg.
func TestListenStream_Item(t *testing.T) {
	ch := make(chan *pb.ServerMessage, 1)
	item := &pb.Item{Id: 99, Title: "Test Item"}
	ch <- &pb.ServerMessage{
		Msg: &pb.ServerMessage_Item{Item: item},
	}
	close(ch)

	sub := client.NewStreamSubForTest(ch, nil)
	cmd := ListenStream(sub)
	msg := cmd()

	itemMsg, ok := msg.(ItemMsg)
	if !ok {
		t.Fatalf("expected ItemMsg, got %T", msg)
	}
	if itemMsg.Item.Id != 99 {
		t.Errorf("expected item ID 99, got %d", itemMsg.Item.Id)
	}
}

// TestListenStream_Reset verifies that a Reset server message becomes ResetMsg.
func TestListenStream_Reset(t *testing.T) {
	ch := make(chan *pb.ServerMessage, 1)
	ch <- &pb.ServerMessage{
		Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}},
	}
	close(ch)

	sub := client.NewStreamSubForTest(ch, nil)
	cmd := ListenStream(sub)
	msg := cmd()

	_, ok := msg.(ResetMsg)
	if !ok {
		t.Fatalf("expected ResetMsg, got %T", msg)
	}
}

// TestListenStream_Complete verifies that a Complete server message becomes CompleteMsg.
func TestListenStream_Complete(t *testing.T) {
	ch := make(chan *pb.ServerMessage, 1)
	ch <- &pb.ServerMessage{
		Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}},
	}
	close(ch)

	sub := client.NewStreamSubForTest(ch, nil)
	cmd := ListenStream(sub)
	msg := cmd()

	_, ok := msg.(CompleteMsg)
	if !ok {
		t.Fatalf("expected CompleteMsg, got %T", msg)
	}
}

// TestListenStream_ChannelClosed_WithError verifies that a closed channel
// with a stream error produces StreamErrorMsg.
func TestListenStream_ChannelClosed_WithError(t *testing.T) {
	ch := make(chan *pb.ServerMessage)
	close(ch)

	testErr := errors.New("stream broken")
	sub := client.NewStreamSubForTest(ch, testErr)
	cmd := ListenStream(sub)
	msg := cmd()

	errMsg, ok := msg.(StreamErrorMsg)
	if !ok {
		t.Fatalf("expected StreamErrorMsg, got %T", msg)
	}
	if errMsg.Err == nil {
		t.Fatal("expected non-nil error in StreamErrorMsg")
	}
	if errMsg.Err.Error() != "stream broken" {
		t.Errorf("expected 'stream broken', got %v", errMsg.Err)
	}
}

// TestListenStream_ChannelClosed_NoError verifies that a cleanly closed channel
// produces StreamErrorMsg with nil error.
func TestListenStream_ChannelClosed_NoError(t *testing.T) {
	ch := make(chan *pb.ServerMessage)
	close(ch)

	sub := client.NewStreamSubForTest(ch, nil)
	cmd := ListenStream(sub)
	msg := cmd()

	errMsg, ok := msg.(StreamErrorMsg)
	if !ok {
		t.Fatalf("expected StreamErrorMsg, got %T", msg)
	}
	if errMsg.Err != nil {
		t.Errorf("expected nil error, got %v", errMsg.Err)
	}
}

// TestListenStream_MultipleMessages verifies sequential reads from the stream.
func TestListenStream_MultipleMessages(t *testing.T) {
	ch := make(chan *pb.ServerMessage, 3)

	// Three messages: Item, Reset, Complete.
	ch <- &pb.ServerMessage{Msg: &pb.ServerMessage_Item{Item: &pb.Item{Id: 1, Title: "First"}}}
	ch <- &pb.ServerMessage{Msg: &pb.ServerMessage_Reset_{Reset_: &pb.Reset{}}}
	ch <- &pb.ServerMessage{Msg: &pb.ServerMessage_Complete{Complete: &pb.Complete{}}}
	close(ch)

	sub := client.NewStreamSubForTest(ch, nil)

	// Message 1: Item
	cmd1 := ListenStream(sub)
	msg1 := cmd1()
	itemMsg, ok := msg1.(ItemMsg)
	if !ok {
		t.Fatalf("msg1: expected ItemMsg, got %T", msg1)
	}
	if itemMsg.Item.Title != "First" {
		t.Errorf("expected 'First', got %s", itemMsg.Item.Title)
	}

	// Message 2: Reset
	cmd2 := ListenStream(sub)
	msg2 := cmd2()
	_, ok = msg2.(ResetMsg)
	if !ok {
		t.Fatalf("msg2: expected ResetMsg, got %T", msg2)
	}

	// Message 3: Complete
	cmd3 := ListenStream(sub)
	msg3 := cmd3()
	_, ok = msg3.(CompleteMsg)
	if !ok {
		t.Fatalf("msg3: expected CompleteMsg, got %T", msg3)
	}

	// Message 4: should be StreamErrorMsg (channel closed, nil error)
	cmd4 := ListenStream(sub)
	msg4 := cmd4()
	errMsg, ok := msg4.(StreamErrorMsg)
	if !ok {
		t.Fatalf("msg4: expected StreamErrorMsg, got %T", msg4)
	}
	if errMsg.Err != nil {
		t.Errorf("expected nil error on clean close, got %v", errMsg.Err)
	}
}
