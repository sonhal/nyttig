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
//	nyttig refresh                               # force immediate fetch of all sources
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
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
	fmt.Fprintf(os.Stderr, "  refresh        Force immediate fetch of all sources\n")
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

// refreshCmd handles the "refresh" subcommand.
func refreshCmd() {
	flags := flag.NewFlagSet("refresh", flag.ExitOnError)
	registerClientFlags(flags)
	flags.Parse(os.Args[2:])

	c := newClient()
	defer c.Close()

	ctx := context.Background()
	// sourceID=0 means refresh all sources.
	if err := c.RefreshSource(ctx, 0); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Refresh triggered for all sources.")
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
