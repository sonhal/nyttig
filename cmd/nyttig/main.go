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
	case "remove-tag":
		removeTagCmd()
	case "add-tag-rule":
		addTagRuleCmd()
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
	fmt.Fprintf(os.Stderr, "  update-source  Update an existing source (name/url/type/refresh/enable/disable)\n")
	fmt.Fprintf(os.Stderr, "  add-tag        Create a new tag\n")
	fmt.Fprintf(os.Stderr, "  list-tags      List all tags\n")
	fmt.Fprintf(os.Stderr, "  remove-tag     Remove a tag (and its rules and assignments) by ID\n")
	fmt.Fprintf(os.Stderr, "  add-tag-rule   Add a regex rule that auto-tags matching items\n")
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

	flags.StringVar(&name, "n", "", "Source display name (required)")
	flags.StringVar(&name, "name", "", "Source display name (required)")
	flags.StringVar(&url, "u", "", "Feed URL (required)")
	flags.StringVar(&url, "url", "", "Feed URL (required)")
	flags.StringVar(&sourceType, "t", "rss", "Feed type: rss or atom")
	flags.StringVar(&sourceType, "type", "rss", "Feed type: rss or atom")
	flags.IntVar(&refreshSec, "r", 3600, "Refresh interval in seconds")
	flags.IntVar(&refreshSec, "refresh", 3600, "Refresh interval in seconds")
	flags.BoolVar(&enabled, "enabled", true, "Enable the source immediately")
	flags.StringVar(&color, "color", "", "Hex color for source chip in TUI (e.g. '#FF6600')")
	flags.StringVar(&abbreviation, "abbreviation", "", "Short display name for TUI (e.g. 'HN')")

	// Parse args starting after "add-source".
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig add-source -n <name> -u <url> [flags]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if name == "" {
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

// updateSourceCmd handles the "update-source" subcommand.
func updateSourceCmd() {
	flags := flag.NewFlagSet("update-source", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id       int64
		name     string
		url      string
		sourceType string
		refresh  int
		enable   bool
		disable  bool
	)

	flags.Int64Var(&id, "i", 0, "Source ID to update (required)")
	flags.Int64Var(&id, "id", 0, "Source ID to update (required)")
	flags.StringVar(&name, "n", "", "New source name")
	flags.StringVar(&name, "name", "", "New source name")
	flags.StringVar(&url, "u", "", "New feed URL")
	flags.StringVar(&url, "url", "", "New feed URL")
	flags.StringVar(&sourceType, "t", "", "New feed type: rss or atom")
	flags.StringVar(&sourceType, "type", "", "New feed type: rss or atom")
	flags.IntVar(&refresh, "r", 0, "New refresh interval in seconds")
	flags.IntVar(&refresh, "refresh", 0, "New refresh interval in seconds")
	flags.BoolVar(&enable, "enable", false, "Enable the source")
	flags.BoolVar(&disable, "disable", false, "Disable the source")

	// Parse args starting after "update-source".
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig update-source -i <id> [flags]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	flags.Parse(args)

	if id == 0 {
		fmt.Fprintf(os.Stderr, "Error: --id (-i) is required\n")
		os.Exit(1)
	}

	if enable && disable {
		fmt.Fprintf(os.Stderr, "Error: --enable and --disable are mutually exclusive\n")
		os.Exit(1)
	}

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	
	// Fetch current source to get existing values
	resp, err := c.ListSources(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var src *pb.Source
	for _, s := range resp.Sources {
		if s.Id == id {
			src = s
			break
		}
	}
	if src == nil {
		fmt.Fprintf(os.Stderr, "Error: source %d not found\n", id)
		os.Exit(1)
	}

	// Build update request with current values
	req := &pb.UpdateSourceRequest{
		Id:         id,
		Name:       src.Name,
		Url:        src.Url,
		Type:       src.Type,
		RefreshSec: src.RefreshSec,
		Enabled:    src.Enabled,
	}

	// Override with user-provided values
	if name != "" {
		req.Name = name
	}
	if url != "" {
		req.Url = url
	}
	if sourceType != "" {
		req.Type = sourceType
	}
	if refresh > 0 {
		req.RefreshSec = int32(refresh)
	}
	if enable {
		req.Enabled = true
	}
	if disable {
		req.Enabled = false
	}

	updated, err := c.UpdateSource(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Source updated: [%d] %s (%s)\n", updated.Id, updated.Name, updated.Url)
}

// addTagCmd handles the "add-tag" subcommand.
func addTagCmd() {
	flags := flag.NewFlagSet("add-tag", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		name  string
		color string
	)

	flags.StringVar(&name, "n", "", "Tag name (required)")
	flags.StringVar(&name, "name", "", "Tag name (required)")
	flags.StringVar(&color, "c", "", "Tag color (hex, e.g. '#FF6B35')")
	flags.StringVar(&color, "color", "", "Tag color (hex, e.g. '#FF6B35')")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		fmt.Fprintf(os.Stderr, "Usage: nyttig add-tag -n <name> [-c <color>]\n\n")
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
	tag, err := c.AddTag(ctx, &pb.AddTagRequest{
		Name:  name,
		Color: color,
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
	for _, tag := range resp.Tags {
		color := tag.Color
		if color == "" {
			color = "-"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\n", tag.Id, tag.Name, color)
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

	fmt.Printf("Tag %d removed (with its rules and item assignments).\n", id)
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
	)

	flags.StringVar(&query, "q", "", "Full-text search query (or pass it as trailing arguments)")
	flags.StringVar(&query, "query", "", "Full-text search query (or pass it as trailing arguments)")
	flags.Int64Var(&sourceID, "s", 0, "Only items from this source ID")
	flags.Int64Var(&sourceID, "source", 0, "Only items from this source ID")
	flags.StringVar(&tagRef, "tag", "", "Only items with this tag (name or ID)")
	flags.IntVar(&limit, "l", 20, "Maximum number of results")
	flags.IntVar(&limit, "limit", 20, "Maximum number of results")
	flags.IntVar(&offset, "offset", 0, "Skip this many results (for paging)")

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
		Query:    query,
		SourceId: sourceID,
		TagId:    tagID,
		Limit:    int32(limit),
		Offset:   int32(offset),
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
			it.Id, published, it.SourceName, tags, oneLine(it.Title), it.Link)
	}
	w.Flush()

	if shown := offset + len(resp.Items); int(resp.Total) > shown {
		fmt.Printf("\nShowing %d–%d of %d. Use -offset %d for more.\n", offset+1, shown, resp.Total, shown)
	}
}

// oneLine collapses whitespace (including newlines and tabs, which would
// break tabwriter columns) into single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
