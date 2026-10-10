package main

import (
	"database/sql"
	"log/slog"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sonhal/nyttig/internal/config"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/fetcher"
)

func openSeedDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// tagTree returns each tag's sorted parent names, read back from the database.
func tagTree(t *testing.T, database *sql.DB) map[string][]string {
	t.Helper()
	tags, err := db.ListTags(database)
	if err != nil {
		t.Fatal(err)
	}
	name := map[int64]string{}
	for _, tg := range tags {
		name[tg.ID] = tg.Name
	}
	tree := map[string][]string{}
	for _, tg := range tags {
		parents := []string{}
		for _, p := range tg.ParentIDs {
			parents = append(parents, name[p])
		}
		sort.Strings(parents)
		tree[tg.Name] = parents
	}
	return tree
}

type logSink struct{ b strings.Builder }

func (l *logSink) Write(p []byte) (int, error) { return l.b.Write(p) }

func captureLogger() (*slog.Logger, *logSink) {
	sink := &logSink{}
	return slog.New(slog.NewTextHandler(sink, nil)), sink
}

func TestSeed_TagParents(t *testing.T) {
	database := openSeedDB(t)
	logger, logs := captureLogger()
	cfg := &config.Config{Tags: []config.Tag{
		// The child comes before its parents, and "linux" is not declared at all.
		{Name: "linux security", Parents: []string{"cyber security", "linux"}},
		{Name: "CVE", Parents: []string{"cyber security"}},
		{Name: "cyber security", Color: "#D7263D"},
	}}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"linux security": {"cyber security", "linux"},
		"CVE":            {"cyber security"},
		"cyber security": {},
		"linux":          {}, // created because a tag named it as a parent
	}
	if got := tagTree(t, database); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
	if strings.Contains(logs.b.String(), "level=WARN") {
		t.Errorf("unexpected warning:\n%s", logs.b.String())
	}

	// Seeding again changes nothing.
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	if got := tagTree(t, database); !reflect.DeepEqual(got, want) {
		t.Errorf("tree after a second seed = %v, want %v", got, want)
	}
}

func TestSeed_TagParentsAreAdditive(t *testing.T) {
	database := openSeedDB(t)
	logger, _ := captureLogger()
	cfg := &config.Config{Tags: []config.Tag{
		{Name: "a"}, {Name: "b"}, {Name: "c", Parents: []string{"a"}},
	}}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}

	// Someone re-parents c in the UI: under b instead of a, plus top-level d.
	tags, _ := db.ListTags(database)
	id := map[string]int64{}
	for _, tg := range tags {
		id[tg.Name] = tg.ID
	}
	if err := db.SetTagParents(database, id["c"], []int64{id["b"]}); err != nil {
		t.Fatal(err)
	}

	// The next start adds the config's edge back and keeps the UI's.
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	if got, want := tagTree(t, database)["c"], []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("c parents = %v, want %v", got, want)
	}

	// Dropping the parent from the config removes nothing.
	cfg.Tags[2].Parents = nil
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	if got, want := tagTree(t, database)["c"], []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("c parents after removing it from the config = %v, want %v", got, want)
	}
}

func TestSeed_TagParentCycleIsSkippedWithWarning(t *testing.T) {
	database := openSeedDB(t)
	logger, logs := captureLogger()
	cfg := &config.Config{Tags: []config.Tag{
		{Name: "a", Parents: []string{"b"}},
		{Name: "b", Parents: []string{"c"}},
		{Name: "c", Parents: []string{"a", "c"}}, // a would close a loop; c is itself
	}}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatalf("a cycle must not fail startup: %v", err)
	}
	want := map[string][]string{"a": {"b"}, "b": {"c"}, "c": {}}
	if got := tagTree(t, database); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
	if n := strings.Count(logs.b.String(), "level=WARN"); n != 2 {
		t.Errorf("got %d warnings, want 2:\n%s", n, logs.b.String())
	}
}

func TestSeed_SampleConfig(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "sample_config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	database := openSeedDB(t)
	logger, _ := captureLogger()
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	tree := tagTree(t, database)
	if got, want := tree["linux security"], []string{"cyber security", "linux"}; !reflect.DeepEqual(got, want) {
		t.Errorf("linux security parents = %v, want %v", got, want)
	}
	if got, want := tree["CVE"], []string{"cyber security"}; !reflect.DeepEqual(got, want) {
		t.Errorf("CVE parents = %v, want %v", got, want)
	}
}

func TestSeed_Assessors(t *testing.T) {
	database := openSeedDB(t)
	logger, logs := captureLogger()
	cfg := &config.Config{Assessors: []config.Assessor{
		{Name: "claude", Description: "importance", Color: "#112233"},
		{Name: "cvss"},
		{Name: ""},
	}}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListAssessors(database)
	if err != nil || len(list) != 2 {
		t.Fatalf("assessors = %v, %v", list, err)
	}
	if list[0].Name != "claude" || list[0].Description != "importance" || list[0].Color != "#112233" {
		t.Errorf("claude = %+v", list[0])
	}
	if n := strings.Count(logs.b.String(), "level=WARN"); n != 1 {
		t.Errorf("got %d warnings, want 1 (the empty name):\n%s", n, logs.b.String())
	}

	// Seeding again neither duplicates nor overwrites an edited assessor.
	edited := list[0]
	edited.Description = "edited by hand"
	if _, err := db.UpdateAssessor(database, edited); err != nil {
		t.Fatal(err)
	}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	again, _ := db.ListAssessors(database)
	if len(again) != 2 || again[0].Description != "edited by hand" {
		t.Errorf("after re-seed: %+v", again)
	}
}

func TestSeed_SampleConfigAssessors(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "sample_config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	database := openSeedDB(t)
	logger, _ := captureLogger()
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "cvss"} {
		if a, err := db.GetAssessorByName(database, name); err != nil || a == nil {
			t.Errorf("assessor %q not seeded: %v, %v", name, a, err)
		}
	}
}

func TestSeed_KEVDefaults(t *testing.T) {
	database := openSeedDB(t)
	logger, logs := captureLogger()
	cfg := &config.Config{Sources: []config.Source{
		{Type: "kev"},
		{Type: "kev", Name: "KEV mirror", URL: "https://mirror.example/kev.json"},
		{Type: "rss", Name: "no url"},
	}}
	if err := seedFromConfig(database, cfg, logger); err != nil {
		t.Fatal(err)
	}
	sources, err := db.ListSources(database)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range sources {
		got[s.Name] = s.Type + " " + s.URL
	}
	want := map[string]string{
		fetcher.KEVDefaultName: "kev " + fetcher.KEVDefaultURL,
		"KEV mirror":           "kev https://mirror.example/kev.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sources = %v, want %v", got, want)
	}
	if n := strings.Count(logs.b.String(), "level=WARN"); n != 1 {
		t.Errorf("got %d warnings, want 1 (the rss source without a url):\n%s", n, logs.b.String())
	}
}
