package service

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// ── Helpers ────────────────────────────────────────────────────────────────

func addRule(t *testing.T, svc *Service, tagID, sourceID int64, field, pattern string) *pb.TagRule {
	t.Helper()
	r, err := svc.AddTagRule(context.Background(), &pb.AddTagRuleRequest{TagId: tagID, SourceId: sourceID, Field: field, Pattern: pattern})
	if err != nil {
		t.Fatalf("AddTagRule(%q): %v", pattern, err)
	}
	return r
}

func removeRule(t *testing.T, svc *Service, id int64) {
	t.Helper()
	if _, err := svc.RemoveTagRule(context.Background(), &pb.RemoveTagRuleRequest{Id: id}); err != nil {
		t.Fatalf("RemoveTagRule(%d): %v", id, err)
	}
}

func applyRules(t *testing.T, svc *Service, req *pb.ApplyTagRulesRequest) *pb.ApplyTagRulesResponse {
	t.Helper()
	resp, err := svc.ApplyTagRules(context.Background(), req)
	if err != nil {
		t.Fatalf("ApplyTagRules(%v): %v", req, err)
	}
	return resp
}

// taggedTitles returns the titles of the items that carry the tag itself
// (not a descendant), sorted.
func taggedTitles(t *testing.T, database *sql.DB, tagID int64) []string {
	t.Helper()
	items, _, err := db.ListItems(database, db.ItemFilter{TagID: tagID, TagExact: true, Limit: 1000})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	out := []string{}
	for _, it := range items {
		out = append(out, it.Title)
	}
	sort.Strings(out)
	return out
}

func wantTagged(t *testing.T, database *sql.DB, tagID int64, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	sort.Strings(want)
	if got := taggedTitles(t, database, tagID); !reflect.DeepEqual(got, want) {
		t.Errorf("tag %d items = %v, want %v", tagID, got, want)
	}
}

// counts flattens a response's per-tag counts for comparison.
func counts(c []*pb.TagSyncCount) map[string][2]int32 {
	out := map[string][2]int32{}
	for _, x := range c {
		out[x.TagName] = [2]int32{x.Added, x.Removed}
	}
	return out
}

// ── Tests ──────────────────────────────────────────────────────────────────

// Items stored before a rule exists stay untagged until ApplyTagRules runs,
// which then tags the matching ones and only those.
func TestApplyTagRules_Backfill(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "Rust 1.90", "", 1)
	insertItem(t, database, src.Id, "Weekly news", "all about rust", 2)
	insertItem(t, database, src.Id, "Go 1.26", "", 3)
	rust := addTag(t, svc, "rust")
	addRule(t, svc, rust.Id, 0, "both", `(?i)\brust\b`)

	wantTagged(t, database, rust.Id) // adding the rule retags nothing

	resp := applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	if resp.ItemsScanned != 3 || resp.ItemsChanged != 2 || len(resp.Skipped) != 0 {
		t.Errorf("response = %v", resp)
	}
	if got := counts(resp.Tags); !reflect.DeepEqual(got, map[string][2]int32{"rust": {2, 0}}) {
		t.Errorf("counts = %v", got)
	}
	wantTagged(t, database, rust.Id, "Rust 1.90", "Weekly news")

	// A second run has nothing to do.
	again := applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	if again.ItemsChanged != 0 || len(again.Tags) != 0 {
		t.Errorf("second run = %v, want no changes", again)
	}
}

func TestApplyTagRules_PerSourceRule(t *testing.T) {
	svc, database := newTestService(t)
	a := addSource(t, svc, "a", "https://example.com/a")
	b := addSource(t, svc, "b", "https://example.com/b")
	insertItem(t, database, a.Id, "a: rust", "", 1)
	insertItem(t, database, b.Id, "b: rust", "", 2)
	rust := addTag(t, svc, "rust")
	addRule(t, svc, rust.Id, b.Id, "title", `rust`)

	applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	wantTagged(t, database, rust.Id, "b: rust")
}

// Removing a rule changes nothing until the next run; the run then keeps
// what the remaining rules match and removes the rest.
func TestApplyTagRules_RemovedRules(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "rust", "", 1)
	insertItem(t, database, src.Id, "cargo", "", 2)
	insertItem(t, database, src.Id, "rust and cargo", "", 3)
	tag := addTag(t, svc, "rust")
	r1 := addRule(t, svc, tag.Id, 0, "title", `rust`)
	r2 := addRule(t, svc, tag.Id, 0, "title", `cargo`)
	applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	wantTagged(t, database, tag.Id, "rust", "cargo", "rust and cargo")

	removeRule(t, svc, r2.Id)
	wantTagged(t, database, tag.Id, "rust", "cargo", "rust and cargo")
	resp := applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	if got := counts(resp.Tags); !reflect.DeepEqual(got, map[string][2]int32{"rust": {0, 1}}) {
		t.Errorf("counts = %v", got)
	}
	wantTagged(t, database, tag.Id, "rust", "rust and cargo")

	// With the last rule gone the tag has no rules, and a run removes all
	// its rows.
	removeRule(t, svc, r1.Id)
	applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	wantTagged(t, database, tag.Id)
}

// The web app edits a rule by adding the new pattern and removing the old
// one; after a run the tag holds exactly the new pattern's items.
func TestApplyTagRules_EditedRule(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "golang news", "", 1)
	insertItem(t, database, src.Id, "go 1.26", "", 2)
	tag := addTag(t, svc, "go")
	old := addRule(t, svc, tag.Id, 0, "title", `golang`)
	applyRules(t, svc, &pb.ApplyTagRulesRequest{})

	addRule(t, svc, tag.Id, 0, "title", `^go `)
	removeRule(t, svc, old.Id)
	applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	wantTagged(t, database, tag.Id, "go 1.26")
}

func TestApplyTagRules_Scope(t *testing.T) {
	svc, database := newTestService(t)
	a := addSource(t, svc, "a", "https://example.com/a")
	b := addSource(t, svc, "b", "https://example.com/b")
	insertItem(t, database, a.Id, "a rust go", "", 1)
	insertItem(t, database, b.Id, "b rust go", "", 2)
	rust := addTag(t, svc, "rust")
	golang := addTag(t, svc, "go")
	addRule(t, svc, rust.Id, 0, "title", `rust`)
	addRule(t, svc, golang.Id, 0, "title", `go`)

	t.Run("one tag", func(t *testing.T) {
		resp := applyRules(t, svc, &pb.ApplyTagRulesRequest{TagId: rust.Id})
		if got := counts(resp.Tags); !reflect.DeepEqual(got, map[string][2]int32{"rust": {2, 0}}) {
			t.Errorf("counts = %v", got)
		}
		wantTagged(t, database, rust.Id, "a rust go", "b rust go")
		wantTagged(t, database, golang.Id) // other tags untouched
	})

	t.Run("one source", func(t *testing.T) {
		resp := applyRules(t, svc, &pb.ApplyTagRulesRequest{SourceId: a.Id})
		if resp.ItemsScanned != 1 {
			t.Errorf("items_scanned = %d, want 1", resp.ItemsScanned)
		}
		wantTagged(t, database, golang.Id, "a rust go") // source b untouched
	})

	t.Run("a source scope never removes other sources' rows", func(t *testing.T) {
		// Drop the go rule: a run on source a removes only a's go row.
		rules, _ := svc.ListTagRules(context.Background(), nil)
		for _, r := range rules.Rules {
			if r.TagId == golang.Id {
				removeRule(t, svc, r.Id)
			}
		}
		if err := db.AssignTagToItem(database, mustItemID(t, database, "b rust go"), golang.Id); err != nil {
			t.Fatal(err)
		}
		applyRules(t, svc, &pb.ApplyTagRulesRequest{SourceId: a.Id})
		wantTagged(t, database, golang.Id, "b rust go")
		wantTagged(t, database, rust.Id, "a rust go", "b rust go")
	})
}

func mustItemID(t *testing.T, database *sql.DB, title string) int64 {
	t.Helper()
	var id int64
	if err := database.QueryRow(`SELECT id FROM items WHERE title = ?`, title).Scan(&id); err != nil {
		t.Fatalf("item %q: %v", title, err)
	}
	return id
}

// A dry run reports exactly what a real run does, and changes and pushes
// nothing.
func TestApplyTagRules_DryRun(t *testing.T) {
	svc, database := newTestService(t)
	client, _ := startGRPC(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "rust", "", 1)
	insertItem(t, database, src.Id, "stale", "", 2)
	marker := insertItem(t, database, src.Id, "marker", "", 3)
	rust := addTag(t, svc, "rust")
	addRule(t, svc, rust.Id, 0, "title", `rust`)
	if err := db.AssignTagToItem(database, mustItemID(t, database, "stale"), rust.Id); err != nil {
		t.Fatal(err)
	}
	stream := openStream(t, ctx, client)

	dry := applyRules(t, svc, &pb.ApplyTagRulesRequest{DryRun: true})
	if got := counts(dry.Tags); !reflect.DeepEqual(got, map[string][2]int32{"rust": {1, 1}}) || dry.ItemsChanged != 2 {
		t.Errorf("dry run = %v", dry)
	}
	wantTagged(t, database, rust.Id, "stale")

	// Nothing was pushed: the next message is the assessment's update.
	claude := addAssessor(t, svc, "claude")
	score := 0.5
	if _, err := svc.PutAssessment(context.Background(), &pb.PutAssessmentRequest{ItemId: marker, AssessorId: claude.Id, Score: &score}); err != nil {
		t.Fatal(err)
	}
	if got := recvUpdate(t, stream).GetItemUpdate().Id; got != marker {
		t.Fatalf("first update is for item %d, want the marker %d", got, marker)
	}

	run := applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	if !reflect.DeepEqual(counts(run.Tags), counts(dry.Tags)) || run.ItemsChanged != dry.ItemsChanged || run.ItemsScanned != dry.ItemsScanned {
		t.Errorf("real run = %v, dry run = %v", run, dry)
	}
	wantTagged(t, database, rust.Id, "rust")
}

// A tag with a rule that does not compile keeps its rows and is reported;
// the other tags are synced.
func TestApplyTagRules_SkipsTagWithInvalidRule(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "kept", "", 1)
	insertItem(t, database, src.Id, "rust", "", 2)
	broken := addTag(t, svc, "broken")
	rust := addTag(t, svc, "rust")
	addRule(t, svc, broken.Id, 0, "title", `rust`)
	// AddTagRule refuses bad patterns, so write it the way config seeding or
	// an older version could have.
	if _, err := db.InsertTagRule(database, &db.TagRule{TagID: broken.Id, Field: "both", Pattern: `[unclosed`}); err != nil {
		t.Fatal(err)
	}
	addRule(t, svc, rust.Id, 0, "title", `rust`)
	if err := db.AssignTagToItem(database, mustItemID(t, database, "kept"), broken.Id); err != nil {
		t.Fatal(err)
	}

	resp := applyRules(t, svc, &pb.ApplyTagRulesRequest{})
	if len(resp.Skipped) != 1 || resp.Skipped[0].TagId != broken.Id || resp.Skipped[0].TagName != "broken" {
		t.Errorf("skipped = %v", resp.Skipped)
	}
	if got := counts(resp.Tags); !reflect.DeepEqual(got, map[string][2]int32{"rust": {1, 0}}) {
		t.Errorf("counts = %v", got)
	}
	wantTagged(t, database, broken.Id, "kept")
	wantTagged(t, database, rust.Id, "rust")
}

func TestApplyTagRules_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	_, err := svc.ApplyTagRules(context.Background(), &pb.ApplyTagRulesRequest{TagId: 999})
	wantCode(t, err, codes.NotFound)
	_, err = svc.ApplyTagRules(context.Background(), &pb.ApplyTagRulesRequest{SourceId: 999})
	wantCode(t, err, codes.NotFound)
}

// A run pushes item_update for each changed item with its new tags, and
// update_matches follows the stream's tag filter.
func TestApplyTagRules_PushesUpdates(t *testing.T) {
	svc, database := newTestService(t)
	client, _ := startGRPC(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	src := addSource(t, svc, "feed", "https://example.com/feed")
	gained := insertItem(t, database, src.Id, "rust", "", 1)
	lost := insertItem(t, database, src.Id, "stale", "", 2)
	insertItem(t, database, src.Id, "untouched", "", 3)
	rust := addTag(t, svc, "rust")
	addRule(t, svc, rust.Id, 0, "title", `rust`)
	if err := db.AssignTagToItem(database, lost, rust.Id); err != nil {
		t.Fatal(err)
	}
	stream, initial := openStreamWith(t, ctx, client, &pb.StreamFilter{TagId: rust.Id})
	if !reflect.DeepEqual(initial, []string{"stale"}) {
		t.Fatalf("initial batch = %v", initial)
	}

	applyRules(t, svc, &pb.ApplyTagRulesRequest{})

	// Changed items are pushed in ID order; the untouched one is not.
	msg := recvUpdate(t, stream)
	up := msg.GetItemUpdate()
	if up.Id != gained || !msg.UpdateMatches || len(up.Tags) != 1 || up.Tags[0].Id != rust.Id {
		t.Fatalf("update for the gained item = %v (matches %v)", up, msg.UpdateMatches)
	}
	msg = recvUpdate(t, stream)
	up = msg.GetItemUpdate()
	if up.Id != lost || msg.UpdateMatches || len(up.Tags) != 0 {
		t.Fatalf("update for the lost item = %v (matches %v)", up, msg.UpdateMatches)
	}
}

// After a run, each tag with a single rule holds exactly the items
// TestTagRule says that rule matches.
func TestApplyTagRules_AgreesWithTestTagRule(t *testing.T) {
	svc, database := newTestService(t)
	a := addSource(t, svc, "a", "https://example.com/a")
	b := addSource(t, svc, "b", "https://example.com/b")
	texts := []struct{ title, desc string }{
		{"Rust 1.90 released", "the compiler"},
		{"Weekly", "rust and go"},
		{"Go 1.26", "generics"},
		{"CVE-2026-1234", "a linux kernel bug"},
		{"Kernel news", "cve fixes"},
		{"Nothing", ""},
	}
	for i, x := range texts {
		insertItem(t, database, a.Id, "a "+x.title, x.desc, i)
		insertItem(t, database, b.Id, "b "+x.title, x.desc, i+len(texts))
	}
	type ruleCase struct {
		tag            string
		source         int64
		field, pattern string
	}
	cases := []ruleCase{
		{"rust", 0, "both", `(?i)\brust\b`},
		{"go", 0, "title", `(?i)\bgo\b`},
		{"cve", b.Id, "both", `(?i)cve`},
		{"kernel", 0, "description", `kernel`},
	}
	tags := map[string]int64{}
	for _, c := range cases {
		tags[c.tag] = addTag(t, svc, c.tag).Id
		addRule(t, svc, tags[c.tag], c.source, c.field, c.pattern)
	}
	applyRules(t, svc, &pb.ApplyTagRulesRequest{})

	for _, c := range cases {
		res, err := svc.TestTagRule(context.Background(), &pb.TestTagRuleRequest{SourceId: c.source, Field: c.field, Pattern: c.pattern, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{}
		for _, it := range res.Items {
			want = append(want, it.Title)
		}
		if len(want) == 0 {
			t.Fatalf("%s: the fixture should match something", c.tag)
		}
		sort.Strings(want)
		if got := taggedTitles(t, database, tags[c.tag]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: tagged %v, TestTagRule matches %v", c.tag, got, want)
		}
	}
}
