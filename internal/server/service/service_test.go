package service

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// ── Helpers ────────────────────────────────────────────────────────────────

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "nyttig.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database), database
}

func wantCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("status code = %v (err %v), want %v", got, err, want)
	}
}

func addSource(t *testing.T, svc *Service, name, url string) *pb.Source {
	t.Helper()
	src, err := svc.AddSource(context.Background(), &pb.AddSourceRequest{Name: name, Url: url, Enabled: true})
	if err != nil {
		t.Fatalf("AddSource(%q): %v", url, err)
	}
	return src
}

// insertItem stores an item published at the given offset from a fixed base
// time, so sort order is deterministic.
func insertItem(t *testing.T, database *sql.DB, sourceID int64, title, desc string, minutes int) int64 {
	t.Helper()
	published := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
	id, _, err := db.InsertItem(database, &db.Item{
		SourceID:    sourceID,
		GUID:        title,
		Link:        "https://example.com/" + title,
		Title:       title,
		Description: &desc,
		Published:   &published,
	})
	if err != nil {
		t.Fatalf("InsertItem(%q): %v", title, err)
	}
	return id
}

func titles(items []*pb.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
	}
	return out
}

// ── Sources ────────────────────────────────────────────────────────────────

func TestAddSource_Validation(t *testing.T) {
	tests := []struct {
		name string
		req  *pb.AddSourceRequest
	}{
		{"empty name", &pb.AddSourceRequest{Name: " ", Url: "https://a.example/feed"}},
		{"name with control char", &pb.AddSourceRequest{Name: "a\x1b[31m", Url: "https://a.example/feed"}},
		{"missing url", &pb.AddSourceRequest{Name: "A"}},
		{"file url", &pb.AddSourceRequest{Name: "A", Url: "file:///etc/passwd"}},
		{"javascript url", &pb.AddSourceRequest{Name: "A", Url: "javascript:alert(1)"}},
		{"relative url", &pb.AddSourceRequest{Name: "A", Url: "/feed.xml"}},
		{"credentials in url", &pb.AddSourceRequest{Name: "A", Url: "https://user:pw@a.example/feed"}},
		{"unknown type", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", Type: "json"}},
		{"refresh too low", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", RefreshSec: 5}},
		{"refresh negative", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", RefreshSec: -1}},
		{"bad color", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", Color: "red; background:url(x)"}},
		{"short color", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", Color: "#FFF"}},
		{"long abbreviation", &pb.AddSourceRequest{Name: "A", Url: "https://a.example/feed", Abbreviation: "ABCDEFGHIJKLMNOPQ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newTestService(t)
			_, err := svc.AddSource(context.Background(), tt.req)
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestAddSource_DefaultsAndDuplicate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{
		Name: "HN", Url: "https://news.example/rss", Enabled: true, Color: "#FF6600", Abbreviation: "HN",
	})
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}
	if src.Type != "rss" || src.RefreshSec != defaultRefreshSec {
		t.Errorf("defaults: type=%q refresh=%d, want rss/%d", src.Type, src.RefreshSec, defaultRefreshSec)
	}
	if src.Color != "#FF6600" || src.Abbreviation != "HN" {
		t.Errorf("color/abbreviation = %q/%q, want #FF6600/HN", src.Color, src.Abbreviation)
	}

	_, err = svc.AddSource(ctx, &pb.AddSourceRequest{Name: "HN again", Url: "https://news.example/rss"})
	wantCode(t, err, codes.AlreadyExists)
}

func TestUpdateSource_PatchesOnlySetFields(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src, err := svc.AddSource(ctx, &pb.AddSourceRequest{
		Name: "HN", Url: "https://news.example/rss", Enabled: true, RefreshSec: 600, Color: "#FF6600", Abbreviation: "HN",
	})
	if err != nil {
		t.Fatalf("AddSource: %v", err)
	}

	// Rename only: nothing else may change, in particular enabled must not
	// flip to false just because it was not sent.
	got, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: src.Id, Name: proto.String("Hacker News")})
	if err != nil {
		t.Fatalf("UpdateSource(name): %v", err)
	}
	if got.Name != "Hacker News" || got.Url != src.Url || got.RefreshSec != 600 || !got.Enabled ||
		got.Color != "#FF6600" || got.Abbreviation != "HN" {
		t.Fatalf("after rename: %+v", got)
	}

	// Explicit false disables; empty strings clear color and abbreviation.
	got, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{
		Id: src.Id, Enabled: proto.Bool(false), Color: proto.String(""), Abbreviation: proto.String(""),
	})
	if err != nil {
		t.Fatalf("UpdateSource(disable/clear): %v", err)
	}
	if got.Enabled || got.Color != "" || got.Abbreviation != "" || got.Name != "Hacker News" {
		t.Fatalf("after disable/clear: %+v", got)
	}
}

func TestUpdateSource_ValidationLeavesSourceUnchanged(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "HN", "https://news.example/rss")

	_, err := svc.UpdateSource(ctx, &pb.UpdateSourceRequest{
		Id: src.Id, Name: proto.String("New"), Url: proto.String("ftp://news.example/rss"),
	})
	wantCode(t, err, codes.InvalidArgument)

	resp, err := svc.ListSources(ctx, nil)
	if err != nil {
		t.Fatalf("ListSources: %v", err)
	}
	if resp.Sources[0].Name != "HN" {
		t.Fatalf("name = %q after rejected update, want HN", resp.Sources[0].Name)
	}

	_, err = svc.UpdateSource(ctx, &pb.UpdateSourceRequest{Id: 999, Name: proto.String("X")})
	wantCode(t, err, codes.NotFound)
}

func TestUpdateSource_DuplicateURL(t *testing.T) {
	svc, _ := newTestService(t)
	a := addSource(t, svc, "A", "https://a.example/rss")
	b := addSource(t, svc, "B", "https://b.example/rss")
	_, err := svc.UpdateSource(context.Background(), &pb.UpdateSourceRequest{Id: b.Id, Url: proto.String(a.Url)})
	wantCode(t, err, codes.AlreadyExists)
}

func TestUpdateSource_NotifiesSchedulerOnlyForFetchChanges(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "HN", "https://news.example/rss")

	var calls []db.Source
	svc.OnSourceUpdated(func(_ context.Context, s db.Source) { calls = append(calls, s) })

	steps := []struct {
		name   string
		req    *pb.UpdateSourceRequest
		notify bool
	}{
		{"rename", &pb.UpdateSourceRequest{Id: src.Id, Name: proto.String("Hacker News")}, false},
		{"recolor", &pb.UpdateSourceRequest{Id: src.Id, Color: proto.String("#123456")}, false},
		{"same enabled", &pb.UpdateSourceRequest{Id: src.Id, Enabled: proto.Bool(true)}, false},
		{"new url", &pb.UpdateSourceRequest{Id: src.Id, Url: proto.String("https://news.example/rss2")}, true},
		{"new interval", &pb.UpdateSourceRequest{Id: src.Id, RefreshSec: proto.Int32(900)}, true},
		{"disable", &pb.UpdateSourceRequest{Id: src.Id, Enabled: proto.Bool(false)}, true},
	}
	for _, step := range steps {
		before := len(calls)
		if _, err := svc.UpdateSource(ctx, step.req); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if notified := len(calls) > before; notified != step.notify {
			t.Errorf("%s: notified = %v, want %v", step.name, notified, step.notify)
		}
	}
	if last := calls[len(calls)-1]; last.Enabled {
		t.Errorf("last notification should carry enabled=false")
	}
}

// ── Tags ───────────────────────────────────────────────────────────────────

func TestAddTag_ValidationAndDuplicate(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: ""})
	wantCode(t, err, codes.InvalidArgument)
	_, err = svc.AddTag(ctx, &pb.AddTagRequest{Name: "rust", Color: "orange"})
	wantCode(t, err, codes.InvalidArgument)

	if _, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "rust"}); err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	_, err = svc.AddTag(ctx, &pb.AddTagRequest{Name: "rust"})
	wantCode(t, err, codes.AlreadyExists)
}

func TestUpdateTag_KeepsRulesAndAssignments(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "A", "https://a.example/rss")

	tag, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "rust", Color: "#FF6B35"})
	if err != nil {
		t.Fatalf("AddTag: %v", err)
	}
	if _, err := svc.AddTagRule(ctx, &pb.AddTagRuleRequest{TagId: tag.Id, Pattern: `(?i)\brust\b`}); err != nil {
		t.Fatalf("AddTagRule: %v", err)
	}
	itemID := insertItem(t, database, src.Id, "Rust 2.0", "", 0)
	if err := db.AssignTagToItem(database, itemID, tag.Id); err != nil {
		t.Fatalf("AssignTagToItem: %v", err)
	}

	got, err := svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: tag.Id, Name: proto.String("rustlang"), Color: proto.String("#000000")})
	if err != nil {
		t.Fatalf("UpdateTag: %v", err)
	}
	if got.Name != "rustlang" || got.Color != "#000000" {
		t.Fatalf("UpdateTag = %+v", got)
	}

	rules, err := svc.ListTagRules(ctx, nil)
	if err != nil {
		t.Fatalf("ListTagRules: %v", err)
	}
	if len(rules.Rules) != 1 || rules.Rules[0].TagName != "rustlang" {
		t.Fatalf("rules after UpdateTag = %+v, want one rule for rustlang", rules.Rules)
	}
	tags, err := db.GetTagsForItem(database, itemID)
	if err != nil {
		t.Fatalf("GetTagsForItem: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "rustlang" {
		t.Fatalf("item tags after UpdateTag = %+v", tags)
	}

	// Clearing the color, leaving the name.
	got, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: tag.Id, Color: proto.String("")})
	if err != nil {
		t.Fatalf("UpdateTag(clear color): %v", err)
	}
	if got.Name != "rustlang" || got.Color != "" {
		t.Fatalf("after clearing color: %+v", got)
	}
}

func TestUpdateTag_Errors(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	a, _ := svc.AddTag(ctx, &pb.AddTagRequest{Name: "a"})
	if _, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "b"}); err != nil {
		t.Fatalf("AddTag: %v", err)
	}

	_, err := svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: 999, Name: proto.String("x")})
	wantCode(t, err, codes.NotFound)
	_, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: a.Id, Name: proto.String("b")})
	wantCode(t, err, codes.AlreadyExists)
	_, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: a.Id, Color: proto.String("#12345")})
	wantCode(t, err, codes.InvalidArgument)
}

// ── Tag rules ──────────────────────────────────────────────────────────────

func TestAddTagRule_Validation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	tag, _ := svc.AddTag(ctx, &pb.AddTagRequest{Name: "go"})

	tests := []struct {
		name string
		req  *pb.AddTagRuleRequest
	}{
		{"no tag", &pb.AddTagRuleRequest{Pattern: "go"}},
		{"empty pattern", &pb.AddTagRuleRequest{TagId: tag.Id}},
		{"bad regex", &pb.AddTagRuleRequest{TagId: tag.Id, Pattern: "(unclosed"}},
		{"backreference (not RE2)", &pb.AddTagRuleRequest{TagId: tag.Id, Pattern: `(a)\1`}},
		{"too long", &pb.AddTagRuleRequest{TagId: tag.Id, Pattern: string(make([]byte, maxPatternLen+1))}},
		{"bad field", &pb.AddTagRuleRequest{TagId: tag.Id, Pattern: "go", Field: "body"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.AddTagRule(ctx, tt.req)
			wantCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestTestTagRule(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	a := addSource(t, svc, "A", "https://a.example/rss")
	b := addSource(t, svc, "B", "https://b.example/rss")
	insertItem(t, database, a.Id, "Go 1.26 released", "", 1)
	insertItem(t, database, a.Id, "Kernel news", "written in golang", 2)
	insertItem(t, database, b.Id, "Golang tips", "", 3)
	insertItem(t, database, b.Id, "Rust news", "", 4)

	tests := []struct {
		name string
		req  *pb.TestTagRuleRequest
		want []string
	}{
		{"both fields, newest first", &pb.TestTagRuleRequest{Pattern: `(?i)\bgo(lang)?\b`},
			[]string{"Golang tips", "Kernel news", "Go 1.26 released"}},
		{"title only", &pb.TestTagRuleRequest{Pattern: `(?i)\bgo(lang)?\b`, Field: "title"},
			[]string{"Golang tips", "Go 1.26 released"}},
		{"description only", &pb.TestTagRuleRequest{Pattern: `(?i)golang`, Field: "description"},
			[]string{"Kernel news"}},
		{"source filter", &pb.TestTagRuleRequest{Pattern: `(?i)\bgo(lang)?\b`, SourceId: b.Id},
			[]string{"Golang tips"}},
		{"limit", &pb.TestTagRuleRequest{Pattern: `(?i)\bgo(lang)?\b`, Limit: 1},
			[]string{"Golang tips"}},
		{"no match", &pb.TestTagRuleRequest{Pattern: `python`}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.TestTagRule(ctx, tt.req)
			if err != nil {
				t.Fatalf("TestTagRule: %v", err)
			}
			if got := titles(resp.Items); fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("matches = %v, want %v", got, tt.want)
			}
		})
	}

	resp, err := svc.TestTagRule(ctx, &pb.TestTagRuleRequest{Pattern: "news"})
	if err != nil {
		t.Fatalf("TestTagRule: %v", err)
	}
	if resp.Scanned != 4 {
		t.Errorf("scanned = %d, want 4", resp.Scanned)
	}

	_, err = svc.TestTagRule(ctx, &pb.TestTagRuleRequest{Pattern: "(bad"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = svc.TestTagRule(ctx, &pb.TestTagRuleRequest{Pattern: "go", Field: "link"})
	wantCode(t, err, codes.InvalidArgument)
}

// ── Search ─────────────────────────────────────────────────────────────────

func TestSearch_SortAndUnviewed(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "A", "https://a.example/rss")
	first := insertItem(t, database, src.Id, "first", "", 1)
	insertItem(t, database, src.Id, "second", "", 2)
	insertItem(t, database, src.Id, "third", "", 3)
	if err := db.MarkViewed(database, []int64{first}); err != nil {
		t.Fatalf("MarkViewed: %v", err)
	}

	tests := []struct {
		name string
		req  *pb.SearchRequest
		want string
	}{
		{"default newest", &pb.SearchRequest{}, "[third second first]"},
		{"oldest", &pb.SearchRequest{Sort: "oldest"}, "[first second third]"},
		{"oldest paged", &pb.SearchRequest{Sort: "oldest", Limit: 1, Offset: 1}, "[second]"},
		{"unviewed only", &pb.SearchRequest{UnviewedOnly: true}, "[third second]"},
		{"unviewed oldest", &pb.SearchRequest{UnviewedOnly: true, Sort: "oldest"}, "[second third]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.Search(ctx, tt.req)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if got := fmt.Sprint(titles(resp.Items)); got != tt.want {
				t.Errorf("items = %s, want %s", got, tt.want)
			}
		})
	}

	_, err := svc.Search(ctx, &pb.SearchRequest{Sort: "random"})
	wantCode(t, err, codes.InvalidArgument)
}

// ── Stream filter ──────────────────────────────────────────────────────────

func TestItemMatchesFilter_Search(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "A", "https://a.example/rss")
	sqliteID := insertItem(t, database, src.Id, "SQLite 3.50 released", "", 1)
	rustID := insertItem(t, database, src.Id, "Rust 1.95", "", 2)

	filter := &pb.StreamFilter{Search: "sqlite"}
	if !svc.itemMatchesFilter(&pb.Item{Id: sqliteID, SourceId: src.Id}, filter) {
		t.Error("pushed SQLite item should match search 'sqlite'")
	}
	if svc.itemMatchesFilter(&pb.Item{Id: rustID, SourceId: src.Id}, filter) {
		t.Error("pushed Rust item should not match search 'sqlite'")
	}
	// FTS5 syntax in the query is quoted, never interpreted.
	if svc.itemMatchesFilter(&pb.Item{Id: rustID, SourceId: src.Id}, &pb.StreamFilter{Search: "sqlite OR rust"}) {
		t.Error("'sqlite OR rust' is a phrase, not an OR query")
	}
	if !svc.itemMatchesFilter(&pb.Item{Id: rustID, SourceId: src.Id}, &pb.StreamFilter{}) {
		t.Error("empty filter should match")
	}
}

// ── Wire format ────────────────────────────────────────────────────────────

// TestSourceColorSurvivesWire guards against the generated code drifting
// from the proto again: protobuf-go serializes from the embedded descriptor,
// so a struct field missing from it is silently dropped on the wire.
func TestSourceColorSurvivesWire(t *testing.T) {
	in := &pb.Source{Id: 1, Name: "HN", Color: "#FF6600", Abbreviation: "HN"}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := &pb.Source{}
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Color != "#FF6600" || out.Abbreviation != "HN" {
		t.Fatalf("after round trip color=%q abbreviation=%q, want #FF6600/HN", out.Color, out.Abbreviation)
	}
}
