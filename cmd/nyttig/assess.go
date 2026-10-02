package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// ── Assessors and assessments ──

// findAssessor picks an assessor out of list by ID (a numeric ref that
// matches one) or by exact name.
func findAssessor(list []*pb.Assessor, ref string) (*pb.Assessor, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, a := range list {
			if a.Id == id {
				return a, nil
			}
		}
	}
	for _, a := range list {
		if a.Name == ref {
			return a, nil
		}
	}
	return nil, fmt.Errorf("assessor %q not found (see 'nyttig list-assessors')", ref)
}

// resolveAssessorID turns an assessor reference (name or ID) into an ID.
func resolveAssessorID(ctx context.Context, c *client.Client, ref string) (int64, error) {
	resp, err := c.ListAssessors(ctx)
	if err != nil {
		return 0, err
	}
	a, err := findAssessor(resp.Assessors, ref)
	if err != nil {
		return 0, err
	}
	return a.Id, nil
}

// formatScore writes a score without trailing zeros: 0.9, 1, 0.35.
func formatScore(s float64) string {
	return strconv.FormatFloat(s, 'f', -1, 64)
}

// formatItemScores lists an item's scores for the search output: each
// assessor's highest score, the selected assessor first and the others by
// name. Assessments without a score are left out.
func formatItemScores(it *pb.Item, selected int64) string {
	best := map[int64]float64{}
	names := map[int64]string{}
	for _, a := range it.Assessments {
		if a.Score == nil {
			continue
		}
		if cur, ok := best[a.AssessorId]; !ok || *a.Score > cur {
			best[a.AssessorId] = *a.Score
		}
		names[a.AssessorId] = a.AssessorName
	}
	ids := make([]int64, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if (ids[i] == selected) != (ids[j] == selected) {
			return ids[i] == selected
		}
		return names[ids[i]] < names[ids[j]]
	})
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%s %.2f", tui.SanitizeLine(names[id]), best[id])
	}
	return strings.Join(parts, ", ")
}

// listAssessorsCmd handles the "list-assessors" subcommand.
func listAssessorsCmd() {
	flags := flag.NewFlagSet("list-assessors", flag.ExitOnError)
	registerClientFlags(flags)
	_ = flags.Parse(os.Args[2:])

	c := newClient()
	defer func() { _ = c.Close() }()

	resp, err := c.ListAssessors(context.Background())
	if err != nil {
		fail(err)
	}
	if len(resp.Assessors) == 0 {
		fmt.Println("No assessors.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\tNAME\tCOLOR\tDESCRIPTION\n")
	_, _ = fmt.Fprintf(w, "--\t----\t-----\t-----------\n")
	for _, a := range resp.Assessors {
		color := a.Color
		if color == "" {
			color = "-"
		}
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", a.Id, tui.SanitizeLine(a.Name), color, tui.SanitizeLine(a.Description))
	}
	_ = w.Flush()
}

// addAssessorCmd handles the "add-assessor" subcommand.
func addAssessorCmd() {
	flags := flag.NewFlagSet("add-assessor", flag.ExitOnError)
	registerClientFlags(flags)

	var name, description, color string
	flags.StringVar(&name, "n", "", "Assessor name (required)")
	flags.StringVar(&name, "name", "", "Assessor name (required)")
	flags.StringVar(&description, "description", "", "What the score means, e.g. 'importance for the tag, 0-1'")
	flags.StringVar(&color, "c", "", "Color (hex, e.g. '#D97757')")
	flags.StringVar(&color, "color", "", "Color (hex, e.g. '#D97757')")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig add-assessor -n <name> [-description <text>] [-c <color>]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if name == "" {
		fail(fmt.Errorf("--name (-n) is required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()

	a, err := c.AddAssessor(context.Background(), &pb.AddAssessorRequest{Name: name, Description: description, Color: color})
	if err != nil {
		fail(err)
	}
	fmt.Printf("Assessor added: [%d] %s\n", a.Id, tui.SanitizeLine(a.Name))
}

// updateAssessorCmd handles the "update-assessor" subcommand. Only the flags
// that were given change anything.
func updateAssessorCmd() {
	flags := flag.NewFlagSet("update-assessor", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id          int64
		ref         string
		rename      string
		description string
		color       string
	)
	flags.Int64Var(&id, "i", 0, "Assessor ID to update (or use -n)")
	flags.Int64Var(&id, "id", 0, "Assessor ID to update (or use -n)")
	flags.StringVar(&ref, "n", "", "Name of the assessor to update (or use -id)")
	flags.StringVar(&ref, "name", "", "Name of the assessor to update (or use -id)")
	flags.StringVar(&rename, "rename", "", "New name")
	flags.StringVar(&description, "description", "", "New description; '' clears it")
	flags.StringVar(&color, "c", "", "New color (hex); '' clears it")
	flags.StringVar(&color, "color", "", "New color (hex); '' clears it")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig update-assessor (-id <id> | -n <name>) [-rename <name>] [-description <text>] [-c <color>]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Only the given flags are changed.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)
	if id == 0 && ref == "" {
		fail(fmt.Errorf("--id (-i) or --name (-n) is required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	if id == 0 {
		id = mustAssessorID(ctx, c, ref)
	}
	req := &pb.UpdateAssessorRequest{Id: id}
	if set["rename"] {
		req.Name = &rename
	}
	if set["description"] {
		req.Description = &description
	}
	if set["c"] || set["color"] {
		req.Color = &color
	}
	a, err := c.UpdateAssessor(ctx, req)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Assessor updated: [%d] %s\n", a.Id, tui.SanitizeLine(a.Name))
}

// mustAssessorID is resolveAssessorID that exits on an error.
func mustAssessorID(ctx context.Context, c *client.Client, ref string) int64 {
	id, err := resolveAssessorID(ctx, c, ref)
	if err != nil {
		fail(err)
	}
	return id
}

// removeAssessorCmd handles the "remove-assessor" subcommand.
func removeAssessorCmd() {
	flags := flag.NewFlagSet("remove-assessor", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id   int64
		name string
	)
	flags.Int64Var(&id, "i", 0, "Assessor ID to remove (or use -n)")
	flags.Int64Var(&id, "id", 0, "Assessor ID to remove (or use -n)")
	flags.StringVar(&name, "n", "", "Name of the assessor to remove (or use -id)")
	flags.StringVar(&name, "name", "", "Name of the assessor to remove (or use -id)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig remove-assessor (-id <id> | -n <name>)\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Also removes all of the assessor's assessments. Saved views that use it\nkeep existing and stop filtering on it.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if id == 0 && name == "" {
		fail(fmt.Errorf("--id (-i) or --name (-n) is required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	if id == 0 {
		id = mustAssessorID(ctx, c, name)
	}
	if err := c.RemoveAssessor(ctx, id); err != nil {
		fail(err)
	}
	fmt.Printf("Assessor %d removed (with its assessments).\n", id)
}

// splitItemArg takes the item ID that "assess" and "unassess" accept as the
// first argument, before the flags (the flag package stops at the first
// non-flag argument).
func splitItemArg(args []string) (item string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}

// parseItemID parses an item ID argument.
func parseItemID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("item id %q is not a positive number", s)
	}
	return id, nil
}

// assessCmd handles the "assess" subcommand.
func assessCmd() {
	flags := flag.NewFlagSet("assess", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		itemFlag string
		assessor string
		tag      string
		score    float64
		note     string
	)
	flags.StringVar(&itemFlag, "item", "", "Item ID (or pass it first)")
	flags.StringVar(&assessor, "assessor", "", "Assessor name or ID (required)")
	flags.StringVar(&tag, "tag", "", "Score the item for this tag (name or ID); omit for the item as a whole")
	flags.Float64Var(&score, "score", 0, "Score from 0 to 1")
	flags.StringVar(&note, "note", "", "Note (plain text)")

	itemArg, args := splitItemArg(os.Args[2:])
	if (itemArg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig assess <item-id> -assessor <name|id> [-tag <name|id>] [-score <0-1>] [-note <text>]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Replaces the assessor's earlier assessment of the item for that tag. A score\nor a note is required.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)
	if itemArg == "" {
		itemArg = itemFlag
	}
	if itemArg == "" {
		itemArg = strings.Join(flags.Args(), "")
	}
	itemID, err := parseItemID(itemArg)
	if err != nil {
		fail(err)
	}
	if assessor == "" {
		fail(fmt.Errorf("--assessor is required"))
	}
	if !set["score"] && note == "" {
		fail(fmt.Errorf("give a --score, a --note or both"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	req := &pb.PutAssessmentRequest{ItemId: itemID, Note: note}
	if req.AssessorId, err = resolveAssessorID(ctx, c, assessor); err != nil {
		fail(err)
	}
	if tag != "" {
		if req.TagId, err = resolveTagID(ctx, c, tag); err != nil {
			fail(err)
		}
	}
	if set["score"] {
		req.Score = &score
	}
	a, err := c.PutAssessment(ctx, req)
	if err != nil {
		fail(err)
	}
	out := fmt.Sprintf("Item %d assessed by %s", a.ItemId, tui.SanitizeLine(a.AssessorName))
	if a.TagId != 0 {
		out += fmt.Sprintf(" for tag %d", a.TagId)
	}
	if a.Score != nil {
		out += ": score " + formatScore(*a.Score)
	}
	fmt.Println(out)
}

// unassessCmd handles the "unassess" subcommand.
func unassessCmd() {
	flags := flag.NewFlagSet("unassess", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		itemFlag string
		assessor string
		tag      string
	)
	flags.StringVar(&itemFlag, "item", "", "Item ID (or pass it first)")
	flags.StringVar(&assessor, "assessor", "", "Assessor name or ID (required)")
	flags.StringVar(&tag, "tag", "", "Remove the assessment for this tag (name or ID); omit for the whole-item one")

	itemArg, args := splitItemArg(os.Args[2:])
	if (itemArg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig unassess <item-id> -assessor <name|id> [-tag <name|id>]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if itemArg == "" {
		itemArg = itemFlag
	}
	if itemArg == "" {
		itemArg = strings.Join(flags.Args(), "")
	}
	itemID, err := parseItemID(itemArg)
	if err != nil {
		fail(err)
	}
	if assessor == "" {
		fail(fmt.Errorf("--assessor is required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	req := &pb.RemoveAssessmentRequest{ItemId: itemID}
	if req.AssessorId, err = resolveAssessorID(ctx, c, assessor); err != nil {
		fail(err)
	}
	if tag != "" {
		if req.TagId, err = resolveTagID(ctx, c, tag); err != nil {
			fail(err)
		}
	}
	if err := c.RemoveAssessment(ctx, req); err != nil {
		fail(err)
	}
	fmt.Printf("Assessment of item %d removed.\n", itemID)
}

// rateCmd handles the "rate" subcommand: a score from you, as the built-in
// assessor "me" (created the first time), for the item as a whole.
func rateCmd() {
	flags := flag.NewFlagSet("rate", flag.ExitOnError)
	registerClientFlags(flags)

	itemArg, args := splitItemArg(os.Args[2:])
	if (itemArg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig rate <item-id> <score 0-1> [note...]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Rates the item as the assessor %q, which is created the first time. Rating\nagain replaces your earlier rating. Your scores are a ground truth to compare\nthe other assessors against.\n\n", client.MeAssessorName)
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	rest := flags.Args()
	if itemArg == "" && len(rest) > 0 {
		itemArg, rest = rest[0], rest[1:]
	}
	itemID, err := parseItemID(itemArg)
	if err != nil {
		fail(err)
	}
	if len(rest) == 0 {
		fail(fmt.Errorf("a score from 0 to 1 is required: nyttig rate <item-id> <score> [note...]"))
	}
	score, err := parseScoreArg(rest[0])
	if err != nil {
		fail(err)
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	a, err := c.Rate(context.Background(), itemID, score, strings.Join(rest[1:], " "))
	if err != nil {
		fail(err)
	}
	fmt.Printf("Item %d rated %s as %s.\n", a.ItemId, formatScore(score), tui.SanitizeLine(a.AssessorName))
}

// parseScoreArg reads a score from 0 to 1: digits with an optional decimal
// point (no exponent, sign, NaN or Inf).
func parseScoreArg(s string) (float64, error) {
	if !scoreArgRe.MatchString(s) {
		return 0, fmt.Errorf("score %q is not a number from 0 to 1", s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1 {
		return 0, fmt.Errorf("score %q is not a number from 0 to 1", s)
	}
	return v, nil
}

var scoreArgRe = regexp.MustCompile(`^(\d+\.?\d*|\.\d+)$`)
