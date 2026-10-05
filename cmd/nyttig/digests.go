package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// ── Digests and digest series ──

// maxBodyRead bounds what -body-file reads: the daemon's limit plus one byte,
// so an oversized body is reported by the daemon, not read whole.
const maxBodyRead = 64*1024 + 1

// findSeries picks a series out of list by ID (a numeric ref that matches
// one) or by "assessor/name" (the name in any case).
func findSeries(list []*pb.DigestSeries, ref string) (*pb.DigestSeries, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, s := range list {
			if s.Id == id {
				return s, nil
			}
		}
	}
	for _, s := range list {
		if strings.EqualFold(s.AssessorName+"/"+s.Name, ref) && strings.HasPrefix(ref, s.AssessorName+"/") {
			return s, nil
		}
	}
	return nil, fmt.Errorf("series %q not found; use <assessor>/<name> or an ID (see 'nyttig list-series')", ref)
}

// resolveSeriesID turns a series reference into an ID.
func resolveSeriesID(ctx context.Context, c *client.Client, ref string) (int64, error) {
	resp, err := c.ListDigestSeries(ctx, 0)
	if err != nil {
		return 0, err
	}
	s, err := findSeries(resp.Series, ref)
	if err != nil {
		return 0, err
	}
	return s.Id, nil
}

// parseTimeArg reads a time: RFC 3339, or YYYY-MM-DD for midnight UTC (for an
// end, the last second of that day).
func parseTimeArg(s string, end bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		if end {
			t = t.Add(24*time.Hour - time.Second)
		}
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("time %q is neither RFC 3339 nor YYYY-MM-DD", s)
}

// parseIDList reads comma-separated positive IDs; "" is an empty list.
func parseIDList(s string) ([]int64, error) {
	ids := []int64{}
	if strings.TrimSpace(s) == "" {
		return ids, nil
	}
	for _, part := range strings.Split(s, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%q is not a positive id", strings.TrimSpace(part))
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// readBodyFile reads a digest body from a file, or from stdin for "-".
func readBodyFile(path string, stdin io.Reader) (string, error) {
	r := stdin
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, maxBodyRead))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func formatDigestTime(t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}
	return t.AsTime().UTC().Format("2006-01-02 15:04Z")
}

// listSeriesCmd handles the "list-series" subcommand.
func listSeriesCmd() {
	flags := flag.NewFlagSet("list-series", flag.ExitOnError)
	registerClientFlags(flags)
	var assessor string
	flags.StringVar(&assessor, "assessor", "", "Only this assessor's series (name or ID)")
	_ = flags.Parse(os.Args[2:])

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	var assessorID int64
	if assessor != "" {
		assessorID = mustAssessorID(ctx, c, assessor)
	}
	resp, err := c.ListDigestSeries(ctx, assessorID)
	if err != nil {
		fail(err)
	}
	if len(resp.Series) == 0 {
		fmt.Println("No digest series.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\tASSESSOR\tNAME\tDIGESTS\tLATEST\tDESCRIPTION\n")
	_, _ = fmt.Fprintf(w, "--\t--------\t----\t-------\t------\t-----------\n")
	for _, s := range resp.Series {
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\n", s.Id, tui.SanitizeLine(s.AssessorName), tui.SanitizeLine(s.Name),
			s.DigestCount, formatDigestTime(s.LatestPeriodEnd), tui.SanitizeLine(s.Description))
	}
	_ = w.Flush()
}

// addSeriesCmd handles the "add-series" subcommand.
func addSeriesCmd() {
	flags := flag.NewFlagSet("add-series", flag.ExitOnError)
	registerClientFlags(flags)
	var assessor, name, description string
	flags.StringVar(&assessor, "assessor", "", "Assessor name or ID (required)")
	flags.StringVar(&name, "n", "", "Series name (required)")
	flags.StringVar(&name, "name", "", "Series name (required)")
	flags.StringVar(&description, "description", "", "What the series covers")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig add-series -assessor <name|id> -n <name> [-description <text>]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if assessor == "" || name == "" {
		fail(fmt.Errorf("--assessor and --name (-n) are required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	s, err := c.AddDigestSeries(ctx, &pb.AddDigestSeriesRequest{
		AssessorId: mustAssessorID(ctx, c, assessor), Name: name, Description: description,
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("Series added: [%d] %s/%s\n", s.Id, tui.SanitizeLine(s.AssessorName), tui.SanitizeLine(s.Name))
}

// updateSeriesCmd handles the "update-series" subcommand. Only the flags that
// were given change anything.
func updateSeriesCmd() {
	flags := flag.NewFlagSet("update-series", flag.ExitOnError)
	registerClientFlags(flags)
	var name, description string
	flags.StringVar(&name, "n", "", "New name")
	flags.StringVar(&name, "name", "", "New name")
	flags.StringVar(&description, "description", "", "New description; '' clears it")

	ref, args := splitItemArg(os.Args[2:])
	if (ref == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig update-series <series> [-n <name>] [-description <text>]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "<series> is <assessor>/<name> or an ID. Only the given flags are changed.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)
	if ref == "" {
		ref = strings.Join(flags.Args(), " ")
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	id, err := resolveSeriesID(ctx, c, ref)
	if err != nil {
		fail(err)
	}
	req := &pb.UpdateDigestSeriesRequest{Id: id}
	if set["n"] || set["name"] {
		req.Name = &name
	}
	if set["description"] {
		req.Description = &description
	}
	s, err := c.UpdateDigestSeries(ctx, req)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Series updated: [%d] %s/%s\n", s.Id, tui.SanitizeLine(s.AssessorName), tui.SanitizeLine(s.Name))
}

// removeSeriesCmd handles the "remove-series" subcommand.
func removeSeriesCmd() {
	flags := flag.NewFlagSet("remove-series", flag.ExitOnError)
	registerClientFlags(flags)

	ref, args := splitItemArg(os.Args[2:])
	if (ref == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig remove-series <series>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "<series> is <assessor>/<name> or an ID. Also removes all of its digests.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if ref == "" {
		ref = strings.Join(flags.Args(), " ")
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	id, err := resolveSeriesID(ctx, c, ref)
	if err != nil {
		fail(err)
	}
	if err := c.RemoveDigestSeries(ctx, id); err != nil {
		fail(err)
	}
	fmt.Printf("Series %d removed (with its digests).\n", id)
}

// reorderSeriesCmd handles the "reorder-series" subcommand.
func reorderSeriesCmd() {
	flags := flag.NewFlagSet("reorder-series", flag.ExitOnError)
	registerClientFlags(flags)

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig reorder-series <series>...\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Lists every series once (<assessor>/<name> or ID), in the new order.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	cur, err := c.ListDigestSeries(ctx, 0)
	if err != nil {
		fail(err)
	}
	ids := make([]int64, 0, len(flags.Args()))
	for _, ref := range flags.Args() {
		s, err := findSeries(cur.Series, ref)
		if err != nil {
			fail(err)
		}
		ids = append(ids, s.Id)
	}
	resp, err := c.ReorderDigestSeries(ctx, ids)
	if err != nil {
		fail(err)
	}
	names := make([]string, len(resp.Series))
	for i, s := range resp.Series {
		names[i] = tui.SanitizeLine(s.AssessorName) + "/" + tui.SanitizeLine(s.Name)
	}
	fmt.Printf("Series reordered: %s\n", strings.Join(names, ", "))
}

// listDigestsCmd handles the "list-digests" subcommand.
func listDigestsCmd() {
	flags := flag.NewFlagSet("list-digests", flag.ExitOnError)
	registerClientFlags(flags)
	var limit int
	var before int64
	flags.IntVar(&limit, "limit", 20, "Digests to list (at most 100)")
	flags.Int64Var(&before, "before", 0, "List the digests older than this digest ID (paging)")

	ref, args := splitItemArg(os.Args[2:])
	if (ref == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig list-digests <series> [-limit N] [-before ID]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "<series> is <assessor>/<name> or an ID. Newest period first.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if ref == "" {
		ref = strings.Join(flags.Args(), " ")
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	id, err := resolveSeriesID(ctx, c, ref)
	if err != nil {
		fail(err)
	}
	resp, err := c.ListDigests(ctx, &pb.ListDigestsRequest{SeriesId: id, BeforeId: before, Limit: int32(limit)})
	if err != nil {
		fail(err)
	}
	if len(resp.Digests) == 0 {
		fmt.Println("No digests.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\tPERIOD END\tITEMS\tINPUTS\tTITLE\n")
	_, _ = fmt.Fprintf(w, "--\t----------\t-----\t------\t-----\n")
	for _, d := range resp.Digests {
		_, _ = fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\n", d.Id, formatDigestTime(d.PeriodEnd), len(d.Items), len(d.Inputs), tui.SanitizeLine(d.Title))
	}
	_ = w.Flush()
	if resp.HasMore {
		fmt.Printf("More: nyttig list-digests %s -before %d\n", ref, resp.Digests[len(resp.Digests)-1].Id)
	}
}

// showDigestCmd handles the "show-digest" subcommand.
func showDigestCmd() {
	flags := flag.NewFlagSet("show-digest", flag.ExitOnError)
	registerClientFlags(flags)

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig show-digest <digest-id>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Prints the digest, the items it is based on and its input digests. The\nbody is untrusted text: control characters are removed.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	id, err := parseItemID(arg)
	if err != nil {
		fail(fmt.Errorf("digest id %q is not a positive number", arg))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	d, err := c.GetDigest(context.Background(), id)
	if err != nil {
		fail(err)
	}
	fmt.Print(formatDigest(d))
}

// formatDigest renders a digest for the terminal. Everything that came from
// an assessor goes through the sanitizers first.
func formatDigest(d *pb.Digest) string {
	var b strings.Builder
	rfc := func(t *timestamppb.Timestamp) string {
		if t == nil {
			return "-"
		}
		return t.AsTime().UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(&b, "Digest %d: %s\n", d.Id, tui.SanitizeLine(d.Title))
	fmt.Fprintf(&b, "Series:  %s/%s\n", tui.SanitizeLine(d.AssessorName), tui.SanitizeLine(d.SeriesName))
	fmt.Fprintf(&b, "Period:  %s to %s\n", rfc(d.PeriodStart), rfc(d.PeriodEnd))
	fmt.Fprintf(&b, "Created: %s   Updated: %s\n\n", rfc(d.CreatedAt), rfc(d.UpdatedAt))
	body := tui.SanitizeText(d.Body)
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	if len(d.Items) > 0 {
		fmt.Fprintf(&b, "\nBased on %d items:\n", len(d.Items))
		for _, it := range d.Items {
			fmt.Fprintf(&b, "  [#%d] %s (%s) %s\n", it.ItemId, tui.SanitizeLine(it.Title), tui.SanitizeLine(it.SourceName), tui.SanitizeLine(it.Link))
		}
	}
	if len(d.Inputs) > 0 {
		fmt.Fprintf(&b, "\nInputs (%d digests):\n", len(d.Inputs))
		for _, in := range d.Inputs {
			fmt.Fprintf(&b, "  [%d] %s (%s, %s)\n", in.Id, tui.SanitizeLine(in.Title), tui.SanitizeLine(in.SeriesName), formatDigestTime(in.PeriodEnd))
		}
	}
	return b.String()
}

// digestFlags are the flags that add-digest and update-digest share.
type digestFlags struct {
	title, start, end, body, bodyFile, items, inputs string
}

func (f *digestFlags) register(flags *flag.FlagSet) {
	flags.StringVar(&f.title, "title", "", "Digest title")
	flags.StringVar(&f.start, "start", "", "Start of the period covered (RFC 3339 or YYYY-MM-DD, UTC midnight)")
	flags.StringVar(&f.end, "end", "", "End of the period covered (RFC 3339 or YYYY-MM-DD, the end of that day)")
	flags.StringVar(&f.body, "body", "", "Body text (Markdown)")
	flags.StringVar(&f.bodyFile, "body-file", "", "Read the body from this file, or from stdin with -")
	flags.StringVar(&f.items, "items", "", "Comma-separated IDs of the items it is based on")
	flags.StringVar(&f.inputs, "inputs", "", "Comma-separated IDs of the earlier digests it used as input")
}

// resolveBody returns the body from -body or -body-file, and whether either
// was given.
func (f *digestFlags) resolveBody(set map[string]bool) (string, bool, error) {
	if set["body"] && set["body-file"] {
		return "", false, fmt.Errorf("use -body or -body-file, not both")
	}
	if set["body-file"] {
		body, err := readBodyFile(f.bodyFile, os.Stdin)
		return body, true, err
	}
	return f.body, set["body"], nil
}

// addDigestCmd handles the "add-digest" subcommand.
func addDigestCmd() {
	flags := flag.NewFlagSet("add-digest", flag.ExitOnError)
	registerClientFlags(flags)
	var f digestFlags
	f.register(flags)

	ref, args := splitItemArg(os.Args[2:])
	if (ref == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig add-digest <series> -title <text> -start <time> -end <time>\n")
		_, _ = fmt.Fprintf(os.Stderr, "                       (-body <text> | -body-file <path|->) [-items 1,2] [-inputs 4,5]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "<series> is <assessor>/<name> or an ID. Always creates a new digest.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)
	if ref == "" {
		ref = strings.Join(flags.Args(), " ")
	}
	if f.title == "" || f.start == "" || f.end == "" {
		fail(fmt.Errorf("-title, -start and -end are required"))
	}
	start, err := parseTimeArg(f.start, false)
	if err != nil {
		fail(err)
	}
	end, err := parseTimeArg(f.end, true)
	if err != nil {
		fail(err)
	}
	body, given, err := f.resolveBody(set)
	if err != nil {
		fail(err)
	}
	if !given {
		fail(fmt.Errorf("-body or -body-file is required"))
	}
	items, err := parseIDList(f.items)
	if err != nil {
		fail(fmt.Errorf("-items: %w", err))
	}
	inputs, err := parseIDList(f.inputs)
	if err != nil {
		fail(fmt.Errorf("-inputs: %w", err))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	seriesID, err := resolveSeriesID(ctx, c, ref)
	if err != nil {
		fail(err)
	}
	d, err := c.AddDigest(ctx, &pb.AddDigestRequest{
		SeriesId: seriesID, Title: f.title, Body: body,
		PeriodStart: timestamppb.New(start), PeriodEnd: timestamppb.New(end),
		ItemIds: items, InputIds: inputs,
	})
	if err != nil {
		fail(err)
	}
	fmt.Printf("Digest added: [%d] %s (%d items, %d inputs)\n", d.Id, tui.SanitizeLine(d.Title), len(d.Items), len(d.Inputs))
}

// updateDigestCmd handles the "update-digest" subcommand. It overwrites what
// the given flags name; there are no revisions.
func updateDigestCmd() {
	flags := flag.NewFlagSet("update-digest", flag.ExitOnError)
	registerClientFlags(flags)
	var f digestFlags
	f.register(flags)

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig update-digest <digest-id> [-title <text>] [-start <time>] [-end <time>]\n")
		_, _ = fmt.Fprintf(os.Stderr, "                          [-body <text> | -body-file <path|->] [-items 1,2] [-inputs 4,5]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Overwrites the given fields (there are no revisions). -items '' or -inputs ''\nremoves all of those links.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	id, err := parseItemID(arg)
	if err != nil {
		fail(fmt.Errorf("digest id %q is not a positive number", arg))
	}

	req := &pb.UpdateDigestRequest{Id: id}
	if set["title"] {
		req.Title = &f.title
	}
	if set["start"] {
		t, err := parseTimeArg(f.start, false)
		if err != nil {
			fail(err)
		}
		req.PeriodStart = timestamppb.New(t)
	}
	if set["end"] {
		t, err := parseTimeArg(f.end, true)
		if err != nil {
			fail(err)
		}
		req.PeriodEnd = timestamppb.New(t)
	}
	body, given, err := f.resolveBody(set)
	if err != nil {
		fail(err)
	}
	if given {
		req.Body = &body
	}
	if set["items"] {
		ids, err := parseIDList(f.items)
		if err != nil {
			fail(fmt.Errorf("-items: %w", err))
		}
		req.ItemIds = &pb.IDList{Ids: ids}
	}
	if set["inputs"] {
		ids, err := parseIDList(f.inputs)
		if err != nil {
			fail(fmt.Errorf("-inputs: %w", err))
		}
		req.InputIds = &pb.IDList{Ids: ids}
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	d, err := c.UpdateDigest(context.Background(), req)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Digest updated: [%d] %s (%d items, %d inputs)\n", d.Id, tui.SanitizeLine(d.Title), len(d.Items), len(d.Inputs))
}

// removeDigestCmd handles the "remove-digest" subcommand.
func removeDigestCmd() {
	flags := flag.NewFlagSet("remove-digest", flag.ExitOnError)
	registerClientFlags(flags)

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig remove-digest <digest-id>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Also removes it from other digests' inputs.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	id, err := parseItemID(arg)
	if err != nil {
		fail(fmt.Errorf("digest id %q is not a positive number", arg))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	if err := c.RemoveDigest(context.Background(), id); err != nil {
		fail(err)
	}
	fmt.Printf("Digest %d removed.\n", id)
}
