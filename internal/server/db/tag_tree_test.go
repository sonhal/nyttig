package db

import (
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func mustTag(t *testing.T, database *sql.DB, name string) int64 {
	t.Helper()
	id, err := InsertTag(database, name, nil)
	if err != nil {
		t.Fatalf("InsertTag(%q): %v", name, err)
	}
	return id
}

func mustParents(t *testing.T, database *sql.DB, child int64, parents ...int64) {
	t.Helper()
	if err := SetTagParents(database, child, parents); err != nil {
		t.Fatalf("SetTagParents(%d, %v): %v", child, parents, err)
	}
}

// tagItem inserts an item with the given title and tags it.
func tagItem(t *testing.T, database *sql.DB, src int64, title string, tagIDs ...int64) {
	t.Helper()
	id, _, err := InsertItem(database, &Item{SourceID: src, GUID: title, Link: "http://example.com/" + title, Title: title})
	if err != nil {
		t.Fatalf("InsertItem(%q): %v", title, err)
	}
	for _, tag := range tagIDs {
		if err := AssignTagToItem(database, id, tag); err != nil {
			t.Fatalf("AssignTagToItem: %v", err)
		}
	}
}

func listTitles(t *testing.T, database *sql.DB, f ItemFilter) []string {
	t.Helper()
	items, total, err := ListItems(database, f)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	got := titles(items)
	if total != len(got) {
		t.Errorf("total = %d, but %d items returned", total, len(got))
	}
	sort.Strings(got)
	return got
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestListItems_TagFilterIncludesDescendants(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")

	// cyber security -> CVE, linux security;  linux -> linux security
	cyber := mustTag(t, database, "cyber security")
	cve := mustTag(t, database, "CVE")
	linux := mustTag(t, database, "linux")
	linuxSec := mustTag(t, database, "linux security")
	mustParents(t, database, cve, cyber)
	mustParents(t, database, linuxSec, cyber, linux)

	tagItem(t, database, src, "cve-news", cve)
	tagItem(t, database, src, "linsec-news", linuxSec)
	tagItem(t, database, src, "cyber-news", cyber)
	tagItem(t, database, src, "linux-news", linux)
	tagItem(t, database, src, "both", cve, cyber) // tagged with a tag and its ancestor
	tagItem(t, database, src, "untagged")

	for _, tc := range []struct {
		name  string
		tag   int64
		exact bool
		want  []string
	}{
		{"parent includes subtree, once each", cyber, false, []string{"both", "cve-news", "cyber-news", "linsec-news"}},
		{"leaf is exact", cve, false, []string{"both", "cve-news"}},
		{"second parent reaches the diamond tag", linux, false, []string{"linsec-news", "linux-news"}},
		{"exact ignores descendants", cyber, true, []string{"both", "cyber-news"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listTitles(t, database, ItemFilter{TagID: tc.tag, TagExact: tc.exact})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListItems_DeepChain(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")

	const depth = 50
	ids := make([]int64, depth)
	for i := range ids {
		ids[i] = mustTag(t, database, "t"+string(rune('A'+i%26))+string(rune('a'+i/26)))
		if i > 0 {
			mustParents(t, database, ids[i], ids[i-1])
		}
	}
	tagItem(t, database, src, "leaf", ids[depth-1])

	if got := listTitles(t, database, ItemFilter{TagID: ids[0]}); !reflect.DeepEqual(got, []string{"leaf"}) {
		t.Errorf("root of a %d-deep chain: got %v, want [leaf]", depth, got)
	}
	d, err := TagDescendants(database, ids[0])
	if err != nil || len(d) != depth {
		t.Errorf("TagDescendants(root) = %d ids, %v; want %d", len(d), err, depth)
	}
}

func TestSetTagParents_RejectsCycles(t *testing.T) {
	database := openTestDB(t)
	a := mustTag(t, database, "a")
	b := mustTag(t, database, "b")
	c := mustTag(t, database, "c")
	mustParents(t, database, b, a) // a -> b
	mustParents(t, database, c, b) // a -> b -> c

	for _, tc := range []struct {
		name   string
		child  int64
		parent []int64
	}{
		{"self", a, []int64{a}},
		{"direct", a, []int64{b}},
		{"indirect", a, []int64{c}},
		{"one bad parent among good ones", b, []int64{a, c}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, _ := ListTagEdges(database)
			err := SetTagParents(database, tc.child, tc.parent)
			var cyc *ErrTagCycle
			if !errors.As(err, &cyc) {
				t.Fatalf("err = %v, want *ErrTagCycle", err)
			}
			after, _ := ListTagEdges(database)
			if !reflect.DeepEqual(before, after) {
				t.Errorf("edges changed by a rejected call: %v -> %v", before, after)
			}
		})
	}

	// The message names the tags.
	err := SetTagParents(database, a, []int64{c})
	if want := `"a" is already an ancestor of "c"`; err == nil || err.Error() != want {
		t.Errorf("message = %v, want %q", err, want)
	}
}

func TestSetTagParents_ReplacesAndValidates(t *testing.T) {
	database := openTestDB(t)
	a := mustTag(t, database, "a")
	b := mustTag(t, database, "b")
	c := mustTag(t, database, "c")

	mustParents(t, database, c, a, b)
	mustParents(t, database, c, b, b) // replaces; a duplicate is harmless
	tags, err := ListTags(database)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range tags {
		want := []int64(nil)
		if tg.ID == c {
			want = []int64{b}
		}
		if !reflect.DeepEqual(tg.ParentIDs, want) {
			t.Errorf("%s.ParentIDs = %v, want %v", tg.Name, tg.ParentIDs, want)
		}
	}
	mustParents(t, database, c) // empty set makes it top-level
	if edges, _ := ListTagEdges(database); len(edges) != 0 {
		t.Errorf("edges after clearing = %v", edges)
	}

	var nf *ErrTagNotFound
	if err := SetTagParents(database, c, []int64{999}); !errors.As(err, &nf) || nf.ID != 999 {
		t.Errorf("unknown parent: err = %v, want *ErrTagNotFound{999}", err)
	}
	if err := SetTagParents(database, 999, []int64{a}); !errors.As(err, &nf) || nf.ID != 999 {
		t.Errorf("unknown child: err = %v, want *ErrTagNotFound{999}", err)
	}
}

func TestInsertTagWithParents_IsAtomic(t *testing.T) {
	database := openTestDB(t)
	a := mustTag(t, database, "a")

	id, err := InsertTagWithParents(database, "child", nil, []int64{a})
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := TagDescendants(database, a); !reflect.DeepEqual(sortedIDs(d), sortedIDs([]int64{a, id})) {
		t.Errorf("descendants of a = %v, want a and the new tag", d)
	}

	var nf *ErrTagNotFound
	if _, err := InsertTagWithParents(database, "orphan", nil, []int64{999}); !errors.As(err, &nf) {
		t.Fatalf("err = %v, want *ErrTagNotFound", err)
	}
	tags, _ := ListTags(database)
	for _, tg := range tags {
		if tg.Name == "orphan" {
			t.Error("a rejected parent left the tag behind")
		}
	}
}

func TestDeleteTag_KeepsChildrenAndTheirItems(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	cyber := mustTag(t, database, "cyber security")
	linux := mustTag(t, database, "linux")
	cve := mustTag(t, database, "CVE")
	linuxSec := mustTag(t, database, "linux security")
	mustParents(t, database, cve, cyber)
	mustParents(t, database, linuxSec, cyber, linux)
	tagItem(t, database, src, "cve-news", cve)
	tagItem(t, database, src, "linsec-news", linuxSec)

	if ok, err := DeleteTag(database, cyber); err != nil || !ok {
		t.Fatalf("DeleteTag = %v, %v", ok, err)
	}

	tags, err := ListTags(database)
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string][]int64{}
	for _, tg := range tags {
		parents[tg.Name] = tg.ParentIDs
	}
	if len(parents["CVE"]) != 0 {
		t.Errorf("CVE parents = %v, want top-level", parents["CVE"])
	}
	if !reflect.DeepEqual(parents["linux security"], []int64{linux}) {
		t.Errorf("linux security parents = %v, want just linux", parents["linux security"])
	}
	// item_tags of the children are intact.
	if got := listTitles(t, database, ItemFilter{TagID: cve, TagExact: true}); !reflect.DeepEqual(got, []string{"cve-news"}) {
		t.Errorf("CVE items = %v", got)
	}
	if got := listTitles(t, database, ItemFilter{TagID: linux}); !reflect.DeepEqual(got, []string{"linsec-news"}) {
		t.Errorf("linux items = %v", got)
	}
}

func TestIndexes_SubtreeFilterPlan(t *testing.T) {
	database := openTestDB(t)
	plan := queryPlan(t, database, `SELECT i.id FROM items i WHERE i.id IN (SELECT item_id FROM item_tags WHERE tag_id IN (`+SubtreeSQL+`))`, 1)
	joined := ""
	for _, l := range plan {
		joined += l + "\n"
	}
	for _, idx := range []string{"idx_item_tags_tag_id", "idx_tag_parents_parent_id"} {
		if !strings.Contains(joined, idx) {
			t.Errorf("subtree filter plan does not use %s:\n%s", idx, joined)
		}
	}
}
