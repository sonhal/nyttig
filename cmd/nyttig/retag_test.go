package main

import (
	"strings"
	"testing"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestPrintApplyResult(t *testing.T) {
	changes := &pb.ApplyTagRulesResponse{
		ItemsScanned: 612,
		ItemsChanged: 241,
		Tags: []*pb.TagSyncCount{
			{TagName: "golang", Added: 3},
			{TagName: "rust", Added: 58, Removed: 2},
			{TagName: "security", Added: 180},
		},
		Skipped: []*pb.TagSyncCount{{TagName: "legacy"}},
	}
	tests := []struct {
		name   string
		resp   *pb.ApplyTagRulesResponse
		dryRun bool
		want   string
	}{
		{"changes", changes, false, `Scanned 612 items; 3 tags changed on 241 items.
  golang    +3    -0
  rust      +58   -2
  security  +180  -0
Skipped (a rule's pattern doesn't compile): legacy
Open TUIs may miss some of these updates; they show them after a reconnect.
`},
		{"dry run", changes, true, `Scanned 612 items; 3 tags would change on 241 items.
  golang    +3    -0
  rust      +58   -2
  security  +180  -0
Skipped (a rule's pattern doesn't compile): legacy
`},
		{"small run, singular", &pb.ApplyTagRulesResponse{ItemsScanned: 5, ItemsChanged: 1, Tags: []*pb.TagSyncCount{{TagName: "go", Removed: 1}}}, false,
			"Scanned 5 items; 1 tag changed on 1 item.\n  go  +0  -1\n"},
		{"nothing", &pb.ApplyTagRulesResponse{ItemsScanned: 5}, false, "Scanned 5 items. Nothing to change.\n"},
		{"nothing, dry run", &pb.ApplyTagRulesResponse{ItemsScanned: 5}, true, "Scanned 5 items. Nothing would change.\n"},
		{"control characters in a name", &pb.ApplyTagRulesResponse{ItemsScanned: 1, ItemsChanged: 1, Tags: []*pb.TagSyncCount{{TagName: "a\x1b[2Jb", Added: 1}}}, false,
			"Scanned 1 item; 1 tag changed on 1 item.\n  a[2Jb  +1  -0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			printApplyResult(&b, tt.resp, tt.dryRun)
			if b.String() != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", b.String(), tt.want)
			}
		})
	}
}
