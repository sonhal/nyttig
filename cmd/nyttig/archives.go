package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// ── Archives ──

// archiveWaitInterval is how often -wait asks the daemon for the archive.
var archiveWaitInterval = time.Second

// archiveBusy reports whether the daemon is still working on an archive.
func archiveBusy(status string) bool {
	return status == "pending" || status == "running"
}

// waitArchive polls get until the archive is no longer pending or running.
func waitArchive(ctx context.Context, get func(context.Context) (*pb.Archive, error), every time.Duration) (*pb.Archive, error) {
	for {
		a, err := get(ctx)
		if err != nil {
			return nil, err
		}
		if !archiveBusy(a.Status) {
			return a, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(every):
		}
	}
}

// formatBytes writes a size as "12 B", "3.4 KiB", "1.2 MiB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// archiveDetail is the error, or else the note, of an archive as one line.
func archiveDetail(a *pb.Archive) string {
	switch {
	case a.Error != "":
		return tui.SanitizeLine(a.Error)
	case a.Note != "":
		return tui.SanitizeLine(a.Note)
	}
	return ""
}

// archiveFileName is the default file name of a downloaded archive: a slug of
// the title with .html, or .pdf for a PDF.
func archiveFileName(a *pb.Archive) string {
	ext := ".html"
	if mt, _, err := mime.ParseMediaType(a.ContentType); err == nil && mt == "application/pdf" {
		ext = ".pdf"
	}
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range tui.SanitizeLine(a.Title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if r := []rune(slug); len(r) > 60 {
		slug = strings.Trim(string(r[:60]), "-")
	}
	if slug == "" {
		slug = fmt.Sprintf("archive-%d", a.Id)
	}
	return slug + ext
}

// archiveCmd handles the "archive" subcommand.
func archiveCmd() {
	flags := flag.NewFlagSet("archive", flag.ExitOnError)
	registerClientFlags(flags)
	var wait bool
	flags.BoolVar(&wait, "wait", false, "Wait until the archive is done (exit 1 if it failed)")

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig archive <item-id> [-wait]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Saves a copy of the article behind the item's link. Archiving an item again\nreplaces its copy.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	itemID, err := parseItemID(arg)
	if err != nil {
		fail(err)
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	a, err := c.ArchiveItem(ctx, itemID)
	if err != nil {
		fail(err)
	}
	fmt.Printf("Archive %d queued for item %d (%s).\n", a.Id, itemID, a.Status)
	if !wait {
		return
	}
	a, err = waitArchive(ctx, func(ctx context.Context) (*pb.Archive, error) { return c.GetArchive(ctx, a.Id) }, archiveWaitInterval)
	if err != nil {
		fail(err)
	}
	line := fmt.Sprintf("Archive %d %s", a.Id, a.Status)
	if d := archiveDetail(a); d != "" {
		line += ": " + d
	}
	fmt.Println(line)
	if a.Status == "failed" {
		os.Exit(1)
	}
}

// listArchivesCmd handles the "list-archives" subcommand.
func listArchivesCmd() {
	flags := flag.NewFlagSet("list-archives", flag.ExitOnError)
	registerClientFlags(flags)
	var (
		status string
		limit  int
		before int64
	)
	flags.StringVar(&status, "status", "", "Only archives with this status (pending, running, done, partial, failed)")
	flags.IntVar(&limit, "limit", 50, "Archives to list (at most 200)")
	flags.Int64Var(&before, "before", 0, "List the archives older than this archive ID (paging)")
	_ = flags.Parse(os.Args[2:])

	c := newClient()
	defer func() { _ = c.Close() }()
	resp, err := c.ListArchives(context.Background(), status, before, int32(limit))
	if err != nil {
		fail(err)
	}
	if len(resp.Archives) == 0 {
		fmt.Println("No archives.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\tSTATUS\tSIZE\tMISSING\tTITLE\n")
	_, _ = fmt.Fprintf(w, "--\t------\t----\t-------\t-----\n")
	for _, a := range resp.Archives {
		title := tui.SanitizeLine(a.Title)
		if d := archiveDetail(a); d != "" {
			title += "  [" + d + "]"
		}
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\n", a.Id, tui.SanitizeLine(a.Status), formatBytes(a.Size), a.MissingAssets, title)
	}
	_ = w.Flush()
	if resp.HasMore {
		fmt.Printf("More: nyttig list-archives -before %d\n", resp.Archives[len(resp.Archives)-1].Id)
	}
}

// openArchiveFile creates the file an archive is saved to. Without force an
// existing file is an error.
func openArchiveFile(path string, force bool) (*os.File, error) {
	flag := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flag, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%s exists; use -f to overwrite", path)
	}
	return f, err
}

// getArchiveCmd handles the "get-archive" subcommand.
func getArchiveCmd() {
	flags := flag.NewFlagSet("get-archive", flag.ExitOnError)
	registerClientFlags(flags)
	var (
		out   string
		force bool
	)
	flags.StringVar(&out, "o", "", "Output file (default: <title>.html or .pdf in the current directory; - for stdout)")
	flags.BoolVar(&force, "f", false, "Overwrite an existing file")

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig get-archive <archive-id> [-o path|-] [-f]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	id, err := parseItemID(arg)
	if err != nil {
		fail(fmt.Errorf("archive id %q is not a positive number", arg))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	a, err := c.GetArchive(ctx, id)
	if err != nil {
		fail(err)
	}
	if a.Size == 0 {
		fail(fmt.Errorf("archive %d has no saved copy (%s)", id, a.Status))
	}

	if out == "-" {
		if _, err := c.GetArchiveContent(ctx, id, os.Stdout); err != nil {
			fail(err)
		}
		return
	}
	if out == "" {
		out = archiveFileName(a)
	}
	f, err := openArchiveFile(out, force)
	if err != nil {
		fail(err)
	}
	n, err := downloadArchive(ctx, c, id, f)
	if err != nil {
		_ = os.Remove(out)
		fail(err)
	}
	fmt.Printf("Saved archive %d to %s (%s).\n", id, out, formatBytes(n))
}

// downloadArchive streams an archive into f and closes it.
func downloadArchive(ctx context.Context, c *client.Client, id int64, f io.WriteCloser) (int64, error) {
	n, err := c.GetArchiveContent(ctx, id, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// removeArchiveCmd handles the "remove-archive" subcommand.
func removeArchiveCmd() {
	flags := flag.NewFlagSet("remove-archive", flag.ExitOnError)
	registerClientFlags(flags)

	arg, args := splitItemArg(os.Args[2:])
	if (arg == "" && len(args) == 0) || (len(args) > 0 && args[0] == "--help") {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig remove-archive <archive-id>\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Deletes the archive and its saved copy.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)
	if arg == "" {
		arg = strings.Join(flags.Args(), "")
	}
	id, err := parseItemID(arg)
	if err != nil {
		fail(fmt.Errorf("archive id %q is not a positive number", arg))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	if err := c.RemoveArchive(context.Background(), id); err != nil {
		fail(err)
	}
	fmt.Printf("Archive %d removed.\n", id)
}
