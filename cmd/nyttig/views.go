package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sonhal/nyttig/internal/client"
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/tui"
)

// ── Saved views ──

// fail prints an error to stderr and exits with status 1.
func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

// resolveSourceID turns a source reference into a source ID. A numeric
// reference is used as-is; anything else is looked up by exact name or
// abbreviation.
func resolveSourceID(ctx context.Context, c *client.Client, ref string) (int64, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		return id, nil
	}
	resp, err := c.ListSources(ctx)
	if err != nil {
		return 0, err
	}
	for _, s := range resp.Sources {
		if s.Name == ref {
			return s.Id, nil
		}
	}
	for _, s := range resp.Sources {
		if s.Abbreviation != "" && s.Abbreviation == ref {
			return s.Id, nil
		}
	}
	return 0, fmt.Errorf("source %q not found (see 'nyttig list-sources')", ref)
}

// findView picks a view out of views by ID (a numeric ref that matches one)
// or by name, ignoring case like the daemon's uniqueness rule does.
func findView(views []*pb.SavedView, ref string) (*pb.SavedView, error) {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, v := range views {
			if v.Id == id {
				return v, nil
			}
		}
	}
	for _, v := range views {
		if strings.EqualFold(v.Name, ref) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("view %q not found (see 'nyttig list-views')", ref)
}

var operatorPrefix = regexp.MustCompile(`^[A-Za-z]+:`)

func quoteQuery(s string) string {
	return `"` + strings.NewReplacer(`"`, `\"`, `\`, `\\`).Replace(s) + `"`
}

// queryName writes a name as an operator value: bare unless that would be
// read back differently.
func queryName(name string) string {
	if name == "" || strings.ContainsAny(name, " \t\r\n\"\\") || strings.HasPrefix(name, "#") {
		return quoteQuery(name)
	}
	return name
}

// queryWord writes a free-text word, quoted when it would read as an operator.
func queryWord(w string) string {
	if strings.HasPrefix(w, `"`) {
		return quoteQuery(w)
	}
	if m := operatorPrefix.FindString(w); m != "" {
		switch strings.ToLower(strings.TrimSuffix(m, ":")) {
		case "tag", "src", "source", "is", "sort":
			return quoteQuery(w)
		}
	}
	return w
}

// formatViewFilter writes a view's filter the way the web `/` bar does:
// words, src:, tag:, is:unviewed, sort: (the default sort is left out).
// A source or tag with no known name is written as #id.
func formatViewFilter(f *pb.ViewFilter, sources map[int64]string, tags map[int64]string) string {
	if f == nil {
		return ""
	}
	var parts []string
	for _, w := range strings.Fields(f.Search) {
		parts = append(parts, queryWord(w))
	}
	named := func(names map[int64]string, id int64) string {
		if n := tui.SanitizeLine(names[id]); n != "" {
			return queryName(n)
		}
		return "#" + strconv.FormatInt(id, 10)
	}
	if f.SourceId != 0 {
		parts = append(parts, "src:"+named(sources, f.SourceId))
	}
	if f.TagId != 0 {
		parts = append(parts, "tag:"+named(tags, f.TagId))
	}
	if f.UnviewedOnly {
		parts = append(parts, "is:unviewed")
	}
	if f.Sort != "" && f.Sort != "newest" {
		parts = append(parts, "sort:"+f.Sort)
	}
	return strings.Join(parts, " ")
}

// nameMaps loads the source and tag names used to format filters.
func nameMaps(ctx context.Context, c *client.Client) (sources, tags map[int64]string, err error) {
	sr, err := c.ListSources(ctx)
	if err != nil {
		return nil, nil, err
	}
	tr, err := c.ListTags(ctx)
	if err != nil {
		return nil, nil, err
	}
	sources = make(map[int64]string, len(sr.Sources))
	for _, s := range sr.Sources {
		sources[s.Id] = s.Name
	}
	tags = make(map[int64]string, len(tr.Tags))
	for _, t := range tr.Tags {
		tags[t.Id] = t.Name
	}
	return sources, tags, nil
}

// listViewsCmd handles the "list-views" subcommand.
func listViewsCmd() {
	flags := flag.NewFlagSet("list-views", flag.ExitOnError)
	registerClientFlags(flags)
	_ = flags.Parse(os.Args[2:])

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	resp, err := c.ListSavedViews(ctx)
	if err != nil {
		fail(err)
	}
	if len(resp.Views) == 0 {
		fmt.Println("No saved views.")
		return
	}
	sources, tags, err := nameMaps(ctx, c)
	if err != nil {
		fail(err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "ID\tNAME\t★\tFILTER\n")
	_, _ = fmt.Fprintf(w, "--\t----\t-\t------\n")
	for _, v := range resp.Views {
		star := ""
		if v.Favorite {
			star = "★"
		}
		filter := formatViewFilter(v.Filter, sources, tags)
		if filter == "" {
			filter = "(everything)"
		}
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", v.Id, tui.SanitizeLine(v.Name), star, filter)
	}
	_ = w.Flush()
}

// addViewCmd handles the "add-view" subcommand.
func addViewCmd() {
	flags := flag.NewFlagSet("add-view", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		name     string
		query    string
		source   string
		tag      string
		sort     string
		unviewed bool
		favorite bool
	)
	flags.StringVar(&name, "n", "", "View name (required)")
	flags.StringVar(&name, "name", "", "View name (required)")
	flags.StringVar(&query, "q", "", "Full-text search text")
	flags.StringVar(&query, "query", "", "Full-text search text")
	flags.StringVar(&source, "source", "", "Only this source (name or ID)")
	flags.StringVar(&tag, "tag", "", "Only this tag and the tags below it (name or ID)")
	flags.StringVar(&sort, "sort", "", "Sort order: newest (default) or oldest")
	flags.BoolVar(&unviewed, "unviewed", false, "Only items not yet viewed")
	flags.BoolVar(&favorite, "favorite", false, "Show the view as a tab in the web app")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig add-view -n <name> [-q <text>] [-source <name|id>] [-tag <name|id>] [-unviewed] [-sort oldest] [-favorite]\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)

	if name == "" {
		fail(fmt.Errorf("--name (-n) is required"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	f := &pb.ViewFilter{Search: query, Sort: sort, UnviewedOnly: unviewed}
	var err error
	if source != "" {
		if f.SourceId, err = resolveSourceID(ctx, c, source); err != nil {
			fail(err)
		}
	}
	if tag != "" {
		if f.TagId, err = resolveTagID(ctx, c, tag); err != nil {
			fail(err)
		}
	}

	v, err := c.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: name, Filter: f, Favorite: favorite})
	if err != nil {
		fail(err)
	}
	fmt.Printf("View added: [%d] %s\n", v.Id, tui.SanitizeLine(v.Name))
}

// updateViewCmd handles the "update-view" subcommand. Only the flags that
// were given change anything; any filter flag replaces the whole stored
// filter, so the filter flags not given keep the stored values.
func updateViewCmd() {
	flags := flag.NewFlagSet("update-view", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id       int64
		ref      string
		name     string
		query    string
		source   string
		tag      string
		noSource bool
		noTag    bool
		sort     string
		unviewed bool
		favorite bool
	)
	flags.Int64Var(&id, "i", 0, "View ID to update (or use -n)")
	flags.Int64Var(&id, "id", 0, "View ID to update (or use -n)")
	flags.StringVar(&ref, "n", "", "Name of the view to update (or use -id)")
	flags.StringVar(&ref, "name", "", "Name of the view to update (or use -id)")
	flags.StringVar(&name, "rename", "", "New view name")
	flags.StringVar(&query, "q", "", "New search text; '' clears it")
	flags.StringVar(&query, "query", "", "New search text; '' clears it")
	flags.StringVar(&source, "source", "", "Only this source (name or ID)")
	flags.StringVar(&tag, "tag", "", "Only this tag and the tags below it (name or ID)")
	flags.BoolVar(&noSource, "no-source", false, "Stop filtering on a source")
	flags.BoolVar(&noTag, "no-tag", false, "Stop filtering on a tag")
	flags.StringVar(&sort, "sort", "", "Sort order: newest or oldest")
	flags.BoolVar(&unviewed, "unviewed", false, "Only items not yet viewed (-unviewed=false clears it)")
	flags.BoolVar(&favorite, "favorite", false, "Show the view as a tab (-favorite=false removes it)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig update-view (-id <id> | -n <name>) [-rename <name>] [-q <text>] [-source <name|id> | -no-source]\n")
		_, _ = fmt.Fprintf(os.Stderr, "                          [-tag <name|id> | -no-tag] [-sort newest|oldest] [-unviewed[=false]] [-favorite[=false]]\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Only the given flags are changed.\n\n")
		flags.PrintDefaults()
		os.Exit(0)
	}
	_ = flags.Parse(args)
	set := setFlagNames(flags)

	if id == 0 && ref == "" {
		fail(fmt.Errorf("--id (-i) or --name (-n) is required"))
	}
	if set["source"] && noSource {
		fail(fmt.Errorf("-source and -no-source cannot be used together"))
	}
	if set["tag"] && noTag {
		fail(fmt.Errorf("-tag and -no-tag cannot be used together"))
	}

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	// The view's current state: for the ID when -id is used with no filter
	// change we need nothing, otherwise the filter is merged with it.
	filterChange := set["q"] || set["query"] || set["source"] || set["tag"] || noSource || noTag || set["sort"] || set["unviewed"]
	var cur *pb.SavedView
	if id == 0 || filterChange {
		resp, err := c.ListSavedViews(ctx)
		if err != nil {
			fail(err)
		}
		if id != 0 {
			for _, v := range resp.Views {
				if v.Id == id {
					cur = v
				}
			}
			if cur == nil {
				fail(fmt.Errorf("view %d not found (see 'nyttig list-views')", id))
			}
		} else if cur, err = findView(resp.Views, ref); err != nil {
			fail(err)
		}
		id = cur.Id
	}

	req := &pb.UpdateSavedViewRequest{Id: id}
	if set["rename"] {
		req.Name = &name
	}
	if set["favorite"] {
		req.Favorite = &favorite
	}
	if filterChange {
		f := &pb.ViewFilter{}
		if cur.Filter != nil {
			f.Search, f.SourceId, f.TagId = cur.Filter.Search, cur.Filter.SourceId, cur.Filter.TagId
			f.Sort, f.UnviewedOnly = cur.Filter.Sort, cur.Filter.UnviewedOnly
		}
		var err error
		if set["q"] || set["query"] {
			f.Search = query
		}
		if noSource {
			f.SourceId = 0
		}
		if set["source"] {
			if f.SourceId, err = resolveSourceID(ctx, c, source); err != nil {
				fail(err)
			}
		}
		if noTag {
			f.TagId = 0
		}
		if set["tag"] {
			if f.TagId, err = resolveTagID(ctx, c, tag); err != nil {
				fail(err)
			}
		}
		if set["sort"] {
			f.Sort = sort
		}
		if set["unviewed"] {
			f.UnviewedOnly = unviewed
		}
		req.Filter = f
	}

	v, err := c.UpdateSavedView(ctx, req)
	if err != nil {
		fail(err)
	}
	fmt.Printf("View updated: [%d] %s\n", v.Id, tui.SanitizeLine(v.Name))
}

// removeViewCmd handles the "remove-view" subcommand.
func removeViewCmd() {
	flags := flag.NewFlagSet("remove-view", flag.ExitOnError)
	registerClientFlags(flags)

	var (
		id   int64
		name string
	)
	flags.Int64Var(&id, "i", 0, "View ID to remove (or use -n)")
	flags.Int64Var(&id, "id", 0, "View ID to remove (or use -n)")
	flags.StringVar(&name, "n", "", "Name of the view to remove (or use -id)")
	flags.StringVar(&name, "name", "", "Name of the view to remove (or use -id)")

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig remove-view (-id <id> | -n <name>)\n\n")
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
		resp, err := c.ListSavedViews(ctx)
		if err != nil {
			fail(err)
		}
		v, err := findView(resp.Views, name)
		if err != nil {
			fail(err)
		}
		id = v.Id
	}
	if err := c.RemoveSavedView(ctx, id); err != nil {
		fail(err)
	}
	fmt.Printf("View %d removed.\n", id)
}

// reorderViewsCmd handles the "reorder-views" subcommand.
func reorderViewsCmd() {
	flags := flag.NewFlagSet("reorder-views", flag.ExitOnError)
	registerClientFlags(flags)

	args := os.Args[2:]
	if len(args) == 0 || args[0] == "--help" {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: nyttig reorder-views <id|name>...\n\n")
		_, _ = fmt.Fprintf(os.Stderr, "Lists every view once, in the new order.\n")
		os.Exit(0)
	}
	_ = flags.Parse(args)

	c := newClient()
	defer func() { _ = c.Close() }()
	ctx := context.Background()

	cur, err := c.ListSavedViews(ctx)
	if err != nil {
		fail(err)
	}
	ids := make([]int64, 0, len(flags.Args()))
	for _, ref := range flags.Args() {
		v, err := findView(cur.Views, ref)
		if err != nil {
			fail(err)
		}
		ids = append(ids, v.Id)
	}
	resp, err := c.ReorderSavedViews(ctx, ids)
	if err != nil {
		fail(err)
	}
	names := make([]string, len(resp.Views))
	for i, v := range resp.Views {
		names[i] = tui.SanitizeLine(v.Name)
	}
	fmt.Printf("Views reordered: %s\n", strings.Join(names, ", "))
}
