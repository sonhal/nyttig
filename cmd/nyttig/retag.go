package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// streamBuffer is the daemon's per-subscriber buffer (Hub.subscribe). A run
// that changes more items than this can drop updates for an open TUI.
const streamBuffer = 64

// applyTagRulesCmd handles the "apply-tag-rules" subcommand: re-run the
// current tag rules over stored items (docs/retag-plan.md).
func applyTagRulesCmd() {
	flags := flag.NewFlagSet("apply-tag-rules", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		tagRef   string
		sourceID int64
		dryRun   bool
	)
	flags.StringVar(&tagRef, "tag", "", "Only sync this tag (name or ID; default: every tag)")
	flags.Int64Var(&sourceID, "s", 0, "Only sync this source's items (default: every source)")
	flags.Int64Var(&sourceID, "source", 0, "Only sync this source's items (default: every source)")
	flags.BoolVar(&dryRun, "dry-run", false, "Show what would change without changing anything")
	flags.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: nyttig apply-tag-rules [-tag <name|id>] [-source <id>] [-dry-run]\n\n")
		fmt.Fprintf(os.Stderr, "Re-runs the current tag rules over stored items. Tags the rules give are\n")
		fmt.Fprintf(os.Stderr, "added and tags they no longer give are removed, so a tag with no rules\n")
		fmt.Fprintf(os.Stderr, "loses all its items. A tag with a rule whose pattern does not compile is\n")
		fmt.Fprintf(os.Stderr, "left alone. Rules otherwise only tag items as they are fetched.\n\n")
		flags.PrintDefaults()
	}
	_ = flags.Parse(os.Args[2:]) // ExitOnError: exits on bad flags or -help

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	var tagID int64
	if tagRef != "" {
		id, err := resolveTagID(ctx, c, tagRef)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		tagID = id
	}

	resp, err := c.ApplyTagRules(ctx, &pb.ApplyTagRulesRequest{TagId: tagID, SourceId: sourceID, DryRun: dryRun})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	printApplyResult(os.Stdout, resp, dryRun)
}

// printApplyResult writes an ApplyTagRules result for people.
func printApplyResult(out io.Writer, resp *pb.ApplyTagRulesResponse, dryRun bool) {
	changeVerb, nothing := "changed", "Nothing to change."
	if dryRun {
		changeVerb, nothing = "would change", "Nothing would change."
	}
	if len(resp.Tags) == 0 {
		_, _ = fmt.Fprintf(out, "Scanned %d %s. %s\n", resp.ItemsScanned, plural(int(resp.ItemsScanned), "item", "items"), nothing)
	} else {
		_, _ = fmt.Fprintf(out, "Scanned %d %s; %d %s %s on %d %s.\n",
			resp.ItemsScanned, plural(int(resp.ItemsScanned), "item", "items"), len(resp.Tags), plural(len(resp.Tags), "tag", "tags"),
			changeVerb, resp.ItemsChanged, plural(int(resp.ItemsChanged), "item", "items"))
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, t := range resp.Tags {
			_, _ = fmt.Fprintf(w, "  %s\t+%d\t-%d\n", tui.SanitizeLine(t.TagName), t.Added, t.Removed)
		}
		_ = w.Flush()
	}
	if len(resp.Skipped) > 0 {
		names := make([]string, len(resp.Skipped))
		for i, t := range resp.Skipped {
			names[i] = tui.SanitizeLine(t.TagName)
		}
		_, _ = fmt.Fprintf(out, "Skipped (a rule's pattern doesn't compile): %s\n", strings.Join(names, ", "))
	}
	if !dryRun && resp.ItemsChanged > streamBuffer {
		_, _ = fmt.Fprintf(out, "Open TUIs may miss some of these updates; they show them after a reconnect.\n")
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
