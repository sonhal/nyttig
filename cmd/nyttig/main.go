// Nyttig CLI client — connects to the nyttigd daemon over gRPC.
//
// When invoked without subcommands, starts the interactive TUI.
// Subcommands provide headless management of sources, tags, and manual refresh.
//
//   nyttig                                       # launch interactive TUI
//   nyttig add-source -n "HN" -u "https://..."   # add a feed source
//   nyttig list-sources                          # list all sources
//   nyttig remove-source -i 1                    # remove source by ID
//   nyttig add-tag -n "rust" -c "#FF6B35"        # create a tag
//   nyttig refresh                               # force immediate fetch of all sources
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// Global flags shared across all subcommands.
var (
	socketPath string
)

func main() {
	if len(os.Args) < 2 {
		// No subcommand: launch the interactive TUI.
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
	case "refresh":
		refreshCmd()
	case "help", "-h", "--help":
		printHelp()
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
	fmt.Fprintf(os.Stderr, "  add-tag        Create a new tag\n")
	fmt.Fprintf(os.Stderr, "  refresh        Force immediate fetch of all sources\n")
	fmt.Fprintf(os.Stderr, "\nGlobal flags:\n")
	fmt.Fprintf(os.Stderr, "  --socket PATH  Daemon Unix socket path (default: /tmp/nyttig.sock)\n")
}

// newClient creates a gRPC client connected to the daemon.
func newClient() *client.Client {
	sock := socketPath
	if sock == "" {
		sock = "/tmp/nyttig.sock"
	}
	return client.New(client.Options{Addr: sock})
}

// addSourceCmd handles the "add-source" subcommand.
func addSourceCmd() {
	flags := flag.NewFlagSet("add-source", flag.ExitOnError)
	flags.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon socket path")

	var (
		name       string
		url        string
		sourceType string
		refreshSec int
		enabled    bool = true
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
		Name:       name,
		Url:        url,
		Type:       sourceType,
		RefreshSec: int32(refreshSec),
		Enabled:    enabled,
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
	flags.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon socket path")
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
	flags.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon socket path")

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

// addTagCmd handles the "add-tag" subcommand.
func addTagCmd() {
	flags := flag.NewFlagSet("add-tag", flag.ExitOnError)
	flags.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon socket path")

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
	flags.StringVar(&socketPath, "socket", "/tmp/nyttig.sock", "Daemon socket path")
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
	// Check for --socket flag in the global args.
	sock := "/tmp/nyttig.sock"
	for i, a := range os.Args {
		if a == "--socket" && i+1 < len(os.Args) {
			sock = os.Args[i+1]
		}
	}

	c := client.New(client.Options{Addr: sock})
	defer c.Close()

	m := tui.NewModel(c)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}