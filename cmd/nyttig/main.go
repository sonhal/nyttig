// Nyttig CLI client — connects to the nyttigd daemon over gRPC.
//
// When invoked without subcommands, starts the interactive TUI.
// Subcommands provide headless management of sources, tags, and manual refresh.
//
//	nyttig                                       # launch interactive TUI
//	nyttig add-source -n "HN" -u "https://..."   # add a feed source
//	nyttig list-sources                          # list all sources
//	nyttig remove-source -i 1                    # remove source by ID
//	nyttig add-tag -n "rust" -c "#FF6B35"        # create a tag
//	nyttig add-tag -n CVE -parent "cyber security"   # create a tag under a parent
//	nyttig list-tags                             # list all tags
//	nyttig add-tag-rule -tag rust -p '(?i)\brust\b'  # auto-tag matching items
//	nyttig list-tag-rules                        # list all tag rules
//	nyttig search -q "sqlite"                    # full-text search stored items
//	nyttig refresh                               # force immediate fetch of all sources
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// Global flags shared across all subcommands.
var (
	socketPath    string
	tlsCert       string
	tlsKey        string
	tlsCA         string
	tlsServerName string
)

// registerClientFlags registers the connection flags shared by every
// subcommand (and the TUI) on fs.
func registerClientFlags(fs *flag.FlagSet) {
	fs.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon Unix socket path or TCP address (host:port)")
	fs.StringVar(&tlsCert, "tls-cert", "", "Client TLS certificate (PEM); enables mTLS together with -tls-key and -tls-ca")
	fs.StringVar(&tlsKey, "tls-key", "", "Client TLS private key (PEM)")
	fs.StringVar(&tlsCA, "tls-ca", "", "CA bundle (PEM) used to verify the daemon's certificate")
	fs.StringVar(&tlsServerName, "tls-server-name", "", "Override the name verified against the daemon's certificate")
}

func main() {
	// Print the curated help for the explicit help forms before the
	// flag-routing check below; otherwise "-h"/"--help" would be treated
	// as leading flags and launch the TUI.
	if len(os.Args) >= 2 && (os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help") {
		printHelp()
		return
	}

	// Launch the TUI when invoked with no subcommand, or when the first
	// argument is a flag (e.g. "nyttig --socket host:9090 --tls-cert ...").
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		runTUI()
		return
	}

	cmd := os.Args[1]

	switch cmd {
	case "add-source":
		addSourceCmd()
	case "list-sources":
		listSourcesCmd()
	case "remove-source":
		removeSourceCmd()
	case "add-tag":
		addTagCmd()
	case "list-tags":
		listTagsCmd()
	case "update-tag":
		updateTagCmd()
	case "remove-tag":
		removeTagCmd()
	case "add-tag-rule":
		addTagRuleCmd()
	case "test-tag-rule":
		testTagRuleCmd()
	case "list-tag-rules":
		listTagRulesCmd()
	case "remove-tag-rule":
		removeTagRuleCmd()
	case "search":
		searchCmd()
	case "update-source":
		updateSourceCmd()
	case "refresh":
		refreshCmd()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s. Run 'nyttig help' for usage.\n", cmd)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Fprintf(os.Stderr, "Usage: nyttig [command] [flags]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  add-source     Add a new feed source\n")
	fmt.Fprintf(os.Stderr, "  list-sources   List all configured sources\n")
	fmt.Fprintf(os.Stderr, "  remove-source  Remove a source by ID\n")
	fmt.Fprintf(os.Stderr, "  update-source  Update an existing source (only the given flags change)\n")
	fmt.Fprintf(os.Stderr, "  add-tag        Create a new tag\n")
	fmt.Fprintf(os.Stderr, "  list-tags      List all tags\n")
	fmt.Fprintf(os.Stderr, "  update-tag     Rename or recolor a tag (keeps rules and assignments)\n")
	fmt.Fprintf(os.Stderr, "  remove-tag     Remove a tag (and its rules and assignments) by ID\n")
	fmt.Fprintf(os.Stderr, "  add-tag-rule   Add a regex rule that auto-tags matching items\n")
	fmt.Fprintf(os.Stderr, "  test-tag-rule  Show which recent items a pattern would tag (dry run)\n")
	fmt.Fprintf(os.Stderr, "  list-tag-rules List all tag rules\n")
	fmt.Fprintf(os.Stderr, "  remove-tag-rule Remove a tag rule by ID\n")
	fmt.Fprintf(os.Stderr, "  search         Full-text search stored items\n")
	fmt.Fprintf(os.Stderr, "  refresh        Force immediate fetch of all sources (or one with -i)\n")
	fmt.Fprintf(os.Stderr, "\nGlobal flags:\n")
	fmt.Fprintf(os.Stderr, "  --socket PATH         Daemon Unix socket path or TCP address (default: /tmp/nyttig.sock)\n")
	fmt.Fprintf(os.Stderr, "  --tls-cert PATH       Client TLS certificate (PEM) for mTLS\n")
	fmt.Fprintf(os.Stderr, "  --tls-key PATH        Client TLS private key (PEM) for mTLS\n")
	fmt.Fprintf(os.Stderr, "  --tls-ca PATH         CA bundle (PEM) to verify the daemon\n")
	fmt.Fprintf(os.Stderr, "  --tls-server-name N   Override name verified against the daemon certificate\n")
}

// clientOptions builds connection options from the parsed global flags.
func clientOptions() client.Options {
	sock := socketPath
	if sock == "" {
		sock = "/tmp/nyttig.sock"
	}
	return client.Options{
		Addr:       sock,
		TLSCert:    tlsCert,
		TLSKey:     tlsKey,
		TLSCA:      tlsCA,
		ServerName: tlsServerName,
	}
}

// newClient creates a gRPC client connected to the daemon.
func newClient() *client.Client {
	return client.New(clientOptions())
}

// addSourceCmd handles the "add-source" subcommand.
func addSourceCmd() {
	flags := flag.NewFlagSet("add-source", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		name         string
		url          string
		sourceType   string
		refreshSec   int
		enabled      bool = true
		color        string
		abbreviation string
	)

	flags.StringVar(&name, "n", "", "Source display name (required, except for bluesky)")
	flags.StringVar(&name, "name", "", "Source display name (required, except for bluesky)")
	flags.StringVar(&url, "u", "", "Feed URL, or for bluesky a handle, DID or profile URL (required)")
	flags.StringVar(&url, "url", "", "Feed URL, or for bluesky a handle, DID or profile URL (required)")
	flags.StringVar(&sourceType, "t", "rss", "Feed type: rss, atom or bluesky")
	flags.StringVar(&sourceType, "type", "rss", "Feed type: rss, atom or bluesky")
	flags.IntVar(&refreshSec, "r", 3600, "Refresh interval in seconds")
	flags.IntVar(&refreshSec, "refresh", 3600, "Refresh interval in seconds")
	flags.BoolVar(&enabled, "enabled", true, "Enable the source immediately")
	flags.StringVar(&color, "color", "", "Hex color for source chip in TUI (e.g. '#FF6600')")
	flags.StringVar(&abbreviation, "abbreviation", "", "Short display name for TUI (e.g. 'HN')")

	// Parse args starting after "add-source".
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig add-source -n <name> -u <url> [flags]\n       nyttig add-source -t bluesky -u <handle|did|profile url> [-n <name>] [flags]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	// A bluesky source is named after the account when no name is given.
	if name == "" && sourceType != "bluesky" {
		fmt.Fprintf(os.Stderr, "Error: --name (-n) is required\n")
		os.Exit(1)
	}
	if url == "" {
		fmt.Fprintf(os.Stderr, "Error: --url (-u) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	req := &pb.AddSourceRequest{
		Name:         name,
		Url:          url,
		Type:         sourceType,
		RefreshSec:   int32(refreshSec),
		Enabled:      enabled,
		Color:        color,
		Abbreviation: abbreviation,
	}

	src, err := c.AddSource(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Source added: [%d] %s (%s) — %s\n", src.Id, src.Name, src.Url, src.Type)
}

// listSourcesCmd handles the "list-sources" subcommand.
func listSourcesCmd() {
	flags := flag.NewFlagSet("list-sources", flag.ExitOnError)
	registerClientFlags(flags)
	flags.Parse(os.Args[2:])

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	resp, err := c.ListSources(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Sources) == 0 {
		fmt.Println("No sources configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tTYPE\tREFRESH\tENABLED\tURL\n")
	fmt.Fprintf(w, "--\t----\t----\t-------\t-------\t---\n")
	for _, src := range resp.Sources {
		status := "yes"
		if !src.Enabled {
			status = "no"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%ds\t%s\t%s\n",
			src.Id, src.Name, src.Type, src.RefreshSec, status, src.Url)
	}
	w.Flush()
}

// removeSourceCmd handles the "remove-source" subcommand.
func removeSourceCmd() {
	flags := flag.NewFlagSet("remove-source", flag.ExitOnError)
	registerClientFlags(flags)

	var id int64
	flags.Int64Var(&id, "i", 0, "Source ID to remove (required)")
	flags.Int64Var(&id, "id", 0, "Source ID to remove (required)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig remove-source -i <id>\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	if err := c.RemoveSource(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Source %d removed.\n", id)
}

// updateSourceCmd handles the "update-source" subcommand. Only the flags
// given on the command line are sent; everything else is left unchanged.
func updateSourceCmd() {
	flags := flag.NewFlagSet("update-source", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id           int64
		name         string
		url          string
		sourceType   string
		refresh      int
		enable       bool
		disable      bool
		color        string
		abbreviation string
	)

	flags.Int64Var(&id, "i", 0, "Source ID to update (required)")
	flags.Int64Var(&id, "id", 0, "Source ID to update (required)")
	flags.StringVar(&name, "n", "", "New source name")
	flags.StringVar(&name, "name", "", "New source name")
	flags.StringVar(&url, "u", "", "New feed URL")
	flags.StringVar(&url, "url", "", "New feed URL")
	flags.StringVar(&sourceType, "t", "", "New feed type: rss, atom or bluesky")
	flags.StringVar(&sourceType, "type", "", "New feed type: rss, atom or bluesky")
	flags.IntVar(&refresh, "r", 0, "New refresh interval in seconds")
	flags.IntVar(&refresh, "refresh", 0, "New refresh interval in seconds")
	flags.BoolVar(&enable, "enable", false, "Enable the source")
	flags.BoolVar(&disable, "disable", false, "Disable the source")
	flags.StringVar(&color, "color", "", "Hex color for the source chip; '' clears it")
	flags.StringVar(&abbreviation, "abbreviation", "", "Short display name; '' clears it")

	// Parse args starting after "update-source".
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig update-source -i <id> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Only the given flags are changed.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args) // ExitOnError: exits on bad flags
	set := setFlagNames(flags)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}
	if enable && disable {
		fmt.Fprintf(os.Stderr, "Error: --enable and --disable are mutually exclusive\n")
		os.Exit(1)
	}

	req := &pb.UpdateSourceRequest{Id: id}
	if set["n"] || set["name"] {
		req.Name = &name
	}
	if set["u"] || set["url"] {
		req.Url = &url
	}
	if set["t"] || set["type"] {
		req.Type = &sourceType
	}
	if set["r"] || set["refresh"] {
		r := int32(refresh)
		req.RefreshSec = &r
	}
	if enable || disable {
		req.Enabled = &enable
	}
	if set["color"] {
		req.Color = &color
	}
	if set["abbreviation"] {
		req.Abbreviation = &abbreviation
	}

	c := newClient()
	defer func() { _ = c.Close() }()

	updated, err := c.UpdateSource(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Source updated: [%d] %s (%s)\n", updated.Id, updated.Name, updated.Url)
}

// setFlagNames returns the names of the flags that were given on the command
// line, so "not given" can be told apart from "given with the zero value".
func setFlagNames(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// addTagCmd handles the "add-tag" subcommand.
func addTagCmd() {
	flags := flag.NewFlagSet("add-tag", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		name    string
		color   string
		parents stringList
	)

	flags.StringVar(&name, "n", "", "Tag name (required)")
	flags.StringVar(&name, "name", "", "Tag name (required)")
	flags.StringVar(&color, "c", "", "Tag color (hex, e.g. '#FF6B35')")
	flags.StringVar(&color, "color", "", "Tag color (hex, e.g. '#FF6B35')")
	flags.Var(&parents, "parent", "Parent tag, name or ID (repeatable)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig add-tag -n <name> [-c <color>] [-parent <name|id>]...\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if name == "" {
		fmt.Fprintf(os.Stderr, "Error: --name (-n) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	parentIDs, err := resolveTagIDs(ctx, c, parents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	tag, err := c.AddTag(ctx, &pb.AddTagRequest{
		Name:      name,
		Color:     color,
		ParentIds: parentIDs,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tag added: [%d] %s", tag.Id, tag.Name)
	if tag.Color != "" {
		fmt.Printf(" (%s)", tag.Color)
	}
	fmt.Println()
}

// listTagsCmd handles the "list-tags" subcommand.
func listTagsCmd() {
	flags := flag.NewFlagSet("list-tags", flag.ExitOnError)
	registerClientFlags(flags)
	flags.Parse(os.Args[2:])

	c := newClient()
	defer c.Close()

	resp, err := c.ListTags(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Tags) == 0 {
		fmt.Println("No tags configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tCOLOR\n")
	fmt.Fprintf(w, "--\t----\t-----\n")
	for _, row := range tagTreeRows(resp.Tags) {
		color := row.Tag.Color
		if color == "" {
			color = "-"
		}
		name := strings.Repeat("  ", row.Depth) + row.Tag.Name
		if len(row.AlsoUnder) > 0 {
			name += " (also under: " + strings.Join(row.AlsoUnder, ", ") + ")"
		}
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\n", row.Tag.Id, name, color)
	}
	w.Flush()
}

// removeTagCmd handles the "remove-tag" subcommand.
func removeTagCmd() {
	flags := flag.NewFlagSet("remove-tag", flag.ExitOnError)
	registerClientFlags(flags)

	var id int64
	flags.Int64Var(&id, "i", 0, "Tag ID to remove (required)")
	flags.Int64Var(&id, "id", 0, "Tag ID to remove (required)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig remove-tag -i <id>\n\n")
		fmt.Fprintf(os.Stderr, "Also removes the tag's rules and its assignments to items.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	if err := c.RemoveTag(context.Background(), id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tag %d removed (with its rules and item assignments; its child tags are kept).\n", id)
}

// updateTagCmd handles the "update-tag" subcommand.
func updateTagCmd() {
	flags := flag.NewFlagSet("update-tag", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id        int64
		name      string
		color     string
		parents   stringList
		noParents bool
	)

	flags.Int64Var(&id, "i", 0, "Tag ID to update (required)")
	flags.Int64Var(&id, "id", 0, "Tag ID to update (required)")
	flags.StringVar(&name, "n", "", "New tag name")
	flags.StringVar(&name, "name", "", "New tag name")
	flags.StringVar(&color, "c", "", "New tag color (hex, e.g. '#FF6B35'); '' clears it")
	flags.StringVar(&color, "color", "", "New tag color (hex, e.g. '#FF6B35'); '' clears it")
	flags.Var(&parents, "parent", "Parent tag, name or ID (repeatable); replaces the tag's parents")
	flags.BoolVar(&noParents, "no-parents", false, "Make the tag top-level (remove all its parents)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig update-tag -i <id> [-n <name>] [-c <color>] [-parent <name|id>]... [-no-parents]\n\n")
		fmt.Fprintf(os.Stderr, "Keeps the tag's rules and item assignments.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args) // ExitOnError: exits on bad flags
	set := setFlagNames(flags)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}

	if noParents && len(parents) > 0 {
		fmt.Fprintf(os.Stderr, "Error: -parent and -no-parents cannot be used together\n")
		os.Exit(1)
	}

	req := &pb.UpdateTagRequest{Id: id}
	if set["n"] || set["name"] {
		req.Name = &name
	}
	if set["c"] || set["color"] {
		req.Color = &color
	}

	c := newClient()
	defer func() { _ = c.Close() }()

	if noParents || len(parents) > 0 {
		parentIDs, err := resolveTagIDs(context.Background(), c, parents)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		req.Parents = &pb.TagParents{Ids: parentIDs}
	}

	tag, err := c.UpdateTag(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tag updated: [%d] %s", tag.Id, tag.Name)
	if tag.Color != "" {
		fmt.Printf(" (%s)", tag.Color)
	}
	fmt.Println()
}

// resolveTagID turns a tag reference into a tag ID. A numeric reference is
// used as-is; anything else is looked up by exact tag name.
func resolveTagID(ctx context.Context, c *client.Client, ref string) (int64, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return id, nil
	}
	resp, err := c.ListTags(ctx)
	if err != nil {
		return 0, err
	}
	for _, tag := range resp.Tags {
		if tag.Name == ref {
			return tag.Id, nil
		}
	}
	return 0, fmt.Errorf("tag %q not found (create it with 'nyttig add-tag -n %s')", ref, ref)
}

// resolveTagIDs resolves several tag references, as resolveTagID does.
func resolveTagIDs(ctx context.Context, c *client.Client, refs []string) ([]int64, error) {
	var ids []int64
	for _, ref := range refs {
		id, err := resolveTagID(ctx, c, ref)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// addTagRuleCmd handles the "add-tag-rule" subcommand.
func addTagRuleCmd() {
	flags := flag.NewFlagSet("add-tag-rule", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		tagRef   string
		pattern  string
		field    string
		sourceID int64
		priority int
	)

	flags.StringVar(&tagRef, "tag", "", "Tag name or ID to assign (required)")
	flags.StringVar(&pattern, "p", "", "Regex pattern, Go syntax (required)")
	flags.StringVar(&pattern, "pattern", "", "Regex pattern, Go syntax (required)")
	flags.StringVar(&field, "f", "both", "Field to match: title, description, or both")
	flags.StringVar(&field, "field", "both", "Field to match: title, description, or both")
	flags.Int64Var(&sourceID, "s", 0, "Restrict rule to this source ID (default: all sources)")
	flags.Int64Var(&sourceID, "source", 0, "Restrict rule to this source ID (default: all sources)")
	flags.IntVar(&priority, "priority", 0, "Evaluation order (lower = evaluated first)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig add-tag-rule -tag <name|id> -p <pattern> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Rules apply to items fetched after the rule is added.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if tagRef == "" {
		fmt.Fprintf(os.Stderr, "Error: --tag is required\n")
		os.Exit(1)
	}
	if pattern == "" {
		fmt.Fprintf(os.Stderr, "Error: --pattern (-p) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	tagID, err := resolveTagID(ctx, c, tagRef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	rule, err := c.AddTagRule(ctx, &pb.AddTagRuleRequest{
		SourceId: sourceID,
		TagId:    tagID,
		Field:    field,
		Pattern:  pattern,
		Priority: int32(priority),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tag rule added: [%d] %s ← /%s/ on %s\n", rule.Id, rule.TagName, rule.Pattern, rule.Field)
}

// testTagRuleCmd handles the "test-tag-rule" subcommand: a dry run of a
// pattern against recently fetched items.
func testTagRuleCmd() {
	flags := flag.NewFlagSet("test-tag-rule", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		pattern  string
		field    string
		sourceID int64
		limit    int
	)

	flags.StringVar(&pattern, "p", "", "Regex pattern, Go syntax (required)")
	flags.StringVar(&pattern, "pattern", "", "Regex pattern, Go syntax (required)")
	flags.StringVar(&field, "f", "both", "Field to match: title, description, or both")
	flags.StringVar(&field, "field", "both", "Field to match: title, description, or both")
	flags.Int64Var(&sourceID, "s", 0, "Only test items from this source ID")
	flags.Int64Var(&sourceID, "source", 0, "Only test items from this source ID")
	flags.IntVar(&limit, "l", 20, "Maximum number of matches to show")
	flags.IntVar(&limit, "limit", 20, "Maximum number of matches to show")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig test-tag-rule -p <pattern> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Shows which recent items a rule would tag, without saving it.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args) // ExitOnError: exits on bad flags

	if pattern == "" {
		fmt.Fprintf(os.Stderr, "Error: --pattern (-p) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer func() { _ = c.Close() }()

	resp, err := c.TestTagRule(context.Background(), &pb.TestTagRuleRequest{
		SourceId: sourceID,
		Field:    field,
		Pattern:  pattern,
		Limit:    int32(limit),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Items) == 0 {
		fmt.Printf("No matches in the %d most recent items.\n", resp.Scanned)
		return
	}

	for _, it := range resp.Items {
		fmt.Printf("%6d  %s  %s\n", it.Id, tui.SanitizeLine(it.SourceName), tui.SanitizeLine(it.Title))
	}
	fmt.Printf("\n%d match(es) shown, %d most recent items checked.\n", len(resp.Items), resp.Scanned)
}

// listTagRulesCmd handles the "list-tag-rules" subcommand.
func listTagRulesCmd() {
	flags := flag.NewFlagSet("list-tag-rules", flag.ExitOnError)
	registerClientFlags(flags)
	flags.Parse(os.Args[2:])

	c := newClient()
	defer c.Close()

	resp, err := c.ListTagRules(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Rules) == 0 {
		fmt.Println("No tag rules configured.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tTAG\tFIELD\tSOURCE\tPRIORITY\tPATTERN\n")
	fmt.Fprintf(w, "--\t---\t-----\t------\t--------\t-------\n")
	for _, r := range resp.Rules {
		source := "all"
		if r.SourceId != 0 {
			source = strconv.FormatInt(r.SourceId, 10)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%d\t%s\n",
			r.Id, r.TagName, r.Field, source, r.Priority, r.Pattern)
	}
	w.Flush()
}

// removeTagRuleCmd handles the "remove-tag-rule" subcommand.
func removeTagRuleCmd() {
	flags := flag.NewFlagSet("remove-tag-rule", flag.ExitOnError)
	registerClientFlags(flags)

	var id int64
	flags.Int64Var(&id, "i", 0, "Tag rule ID to remove (required)")
	flags.Int64Var(&id, "id", 0, "Tag rule ID to remove (required)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig remove-tag-rule -i <id>\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	if err := c.RemoveTagRule(context.Background(), id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Tag rule %d removed.\n", id)
}

// searchCmd handles the "search" subcommand.
func searchCmd() {
	flags := flag.NewFlagSet("search", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		query    string
		sourceID int64
		tagRef   string
		limit    int
		offset   int
		sort     string
		unviewed bool
		exact    bool
	)

	flags.StringVar(&query, "q", "", "Full-text search query (or pass it as trailing arguments)")
	flags.StringVar(&query, "query", "", "Full-text search query (or pass it as trailing arguments)")
	flags.Int64Var(&sourceID, "s", 0, "Only items from this source ID")
	flags.Int64Var(&sourceID, "source", 0, "Only items from this source ID")
	flags.StringVar(&tagRef, "tag", "", "Only items with this tag or a tag below it (name or ID)")
	flags.BoolVar(&exact, "exact", false, "With -tag: only items with exactly this tag, not its child tags")
	flags.IntVar(&limit, "l", 20, "Maximum number of results")
	flags.IntVar(&limit, "limit", 20, "Maximum number of results")
	flags.IntVar(&offset, "offset", 0, "Skip this many results (for paging)")
	flags.StringVar(&sort, "sort", "newest", "Sort order: newest or oldest")
	flags.BoolVar(&unviewed, "unviewed", false, "Only items not yet viewed")

	args := os.Args[2:]
	if len(args) > 0 && args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig search [flags] [query...]\n\n")
		fmt.Fprintf(os.Stderr, "With no query, lists the newest items matching the source/tag filters.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if rest := flags.Args(); len(rest) > 0 {
		if query != "" {
			query += " "
		}
		query += strings.Join(rest, " ")
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	var tagID int64
	if tagRef != "" {
		var err error
		if tagID, err = resolveTagID(ctx, c, tagRef); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	resp, err := c.Search(ctx, &pb.SearchRequest{
		Query:        query,
		SourceId:     sourceID,
		TagId:        tagID,
		TagExact:     exact,
		Limit:        int32(limit),
		Offset:       int32(offset),
		Sort:         sort,
		UnviewedOnly: unviewed,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Items) == 0 {
		fmt.Println("No matching items.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tPUBLISHED\tSOURCE\tTAGS\tTITLE\tLINK\n")
	fmt.Fprintf(w, "--\t---------\t------\t----\t-----\t----\n")
	for _, it := range resp.Items {
		published := "-"
		if it.Published != nil {
			published = it.Published.AsTime().Local().Format("2006-01-02 15:04")
		}
		tagNames := make([]string, len(it.Tags))
		for i, t := range it.Tags {
			tagNames[i] = t.Name
		}
		tags := strings.Join(tagNames, ",")
		if tags == "" {
			tags = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n",
			it.Id, published, tui.SanitizeLine(it.SourceName), tui.SanitizeLine(tags), tui.SanitizeLine(it.Title), tui.SanitizeLine(it.Link))
	}
	w.Flush()

	if shown := offset + len(resp.Items); int(resp.Total) > shown {
		fmt.Printf("\nShowing %d–%d of %d. Use -offset %d for more.\n", offset+1, shown, resp.Total, shown)
	}
}

// refreshCmd handles the "refresh" subcommand.
func refreshCmd() {
	flags := flag.NewFlagSet("refresh", flag.ExitOnError)
	registerClientFlags(flags)

	var id int64
	flags.Int64Var(&id, "i", 0, "Source ID to refresh (default: all sources)")
	flags.Int64Var(&id, "id", 0, "Source ID to refresh (default: all sources)")
	flags.Parse(os.Args[2:])

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	// sourceID=0 means refresh all sources.
	if err := c.RefreshSource(ctx, id); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if id == 0 {
		fmt.Println("Refresh triggered for all sources.")
	} else {
		fmt.Printf("Refresh triggered for source %d.\n", id)
	}
}

// runTUI starts the interactive Bubble Tea TUI.
func runTUI() {
	fs := flag.NewFlagSet("nyttig", flag.ExitOnError)
	registerClientFlags(fs)
	// os.Args[1:] is safe: when a subcommand is present main() dispatches
	// elsewhere, so anything reaching here is global flags (or nothing).
	fs.Parse(os.Args[1:])

	c := client.New(clientOptions())
	defer c.Close()

	m := tui.NewModel(c)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
