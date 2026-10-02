package service

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

func addTag(t *testing.T, svc *Service, name string, parents ...int64) *pb.Tag {
	t.Helper()
	tag, err := svc.AddTag(context.Background(), &pb.AddTagRequest{Name: name, ParentIds: parents})
	if err != nil {
		t.Fatalf("AddTag(%q): %v", name, err)
	}
	return tag
}

func TestAddTag_Parents(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	a := addTag(t, svc, "a")
	b := addTag(t, svc, "b")
	c := addTag(t, svc, "c", a.Id, b.Id)
	if !reflect.DeepEqual(c.ParentIds, []int64{a.Id, b.Id}) {
		t.Errorf("created tag parent_ids = %v", c.ParentIds)
	}

	list, err := svc.ListTags(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range list.Tags {
		if tg.Name == "c" && !reflect.DeepEqual(tg.ParentIds, []int64{a.Id, b.Id}) {
			t.Errorf("listed parent_ids = %v", tg.ParentIds)
		}
	}

	_, err = svc.AddTag(ctx, &pb.AddTagRequest{Name: "orphan", ParentIds: []int64{999}})
	wantCode(t, err, codes.NotFound)
	if list, _ := svc.ListTags(ctx, nil); len(list.Tags) != 3 {
		t.Errorf("a rejected AddTag left a tag behind: %d tags", len(list.Tags))
	}
}

func TestParentValidation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	a := addTag(t, svc, "a")

	many := make([]int64, maxTagParents+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	for name, ids := range map[string][]int64{
		"zero":      {0},
		"negative":  {-3},
		"duplicate": {a.Id, a.Id},
		"too many":  many,
	} {
		_, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "x", ParentIds: ids})
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
		_, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: a.Id, Parents: &pb.TagParents{Ids: ids}})
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestUpdateTag_ParentsPresenceAndCycles(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	a := addTag(t, svc, "a")
	b := addTag(t, svc, "b", a.Id)
	c := addTag(t, svc, "c", b.Id)

	// Parents unset: unchanged.
	name := "b2"
	got, err := svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: b.Id, Name: &name})
	if err != nil || !reflect.DeepEqual(got.ParentIds, []int64{a.Id}) {
		t.Fatalf("rename: %v, parents %v; want parents kept", err, got.GetParentIds())
	}
	// Set to empty: top-level.
	got, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: b.Id, Parents: &pb.TagParents{}})
	if err != nil || len(got.ParentIds) != 0 {
		t.Fatalf("clear parents: %v, parents %v", err, got.GetParentIds())
	}
	// Re-attach, then try to close a loop.
	if _, err := svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: b.Id, Parents: &pb.TagParents{Ids: []int64{a.Id}}}); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]*pb.UpdateTagRequest{
		"self":     {Id: a.Id, Parents: &pb.TagParents{Ids: []int64{a.Id}}},
		"direct":   {Id: a.Id, Parents: &pb.TagParents{Ids: []int64{b.Id}}},
		"indirect": {Id: a.Id, Parents: &pb.TagParents{Ids: []int64{c.Id}}},
	} {
		_, err := svc.UpdateTag(ctx, req)
		if err == nil {
			t.Errorf("%s: want a cycle error", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}
	// A rejected update changes nothing, not even the name in the same call.
	newName := "renamed"
	_, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: a.Id, Name: &newName, Parents: &pb.TagParents{Ids: []int64{c.Id}}})
	wantCode(t, err, codes.InvalidArgument)
	if tag, _ := db.GetTag(svc.db, a.Id); tag.Name != "a" {
		t.Errorf("name = %q after a rejected update, want a", tag.Name)
	}
	// Unknown parent.
	_, err = svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: a.Id, Parents: &pb.TagParents{Ids: []int64{999}}})
	wantCode(t, err, codes.NotFound)
}

func TestSearch_TagExact(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed")
	parent := addTag(t, svc, "parent")
	child := addTag(t, svc, "child", parent.Id)
	p := insertItem(t, database, src.Id, "p-item", "", 0)
	c := insertItem(t, database, src.Id, "c-item", "", 1)
	_ = db.AssignTagToItem(database, p, parent.Id)
	_ = db.AssignTagToItem(database, c, child.Id)

	resp, err := svc.Search(ctx, &pb.SearchRequest{TagId: parent.Id})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(resp.Items); !reflect.DeepEqual(got, []string{"c-item", "p-item"}) {
		t.Errorf("subtree search = %v", got)
	}
	resp, err = svc.Search(ctx, &pb.SearchRequest{TagId: parent.Id, TagExact: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(resp.Items); !reflect.DeepEqual(got, []string{"p-item"}) {
		t.Errorf("exact search = %v", got)
	}
}

// TestItemMatchesFilter_AgreesWithListItems pushes every item through the
// Hub's filter and compares with what ListItems returns for the same tag, on
// a diamond, before and after the tree changes.
func TestItemMatchesFilter_AgreesWithListItems(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed")

	cyber := addTag(t, svc, "cyber security")
	linux := addTag(t, svc, "linux")
	cve := addTag(t, svc, "CVE", cyber.Id)
	linuxSec := addTag(t, svc, "linux security", cyber.Id, linux.Id)
	deep := addTag(t, svc, "kernel cve", linuxSec.Id)
	lone := addTag(t, svc, "lone")
	all := []*pb.Tag{cyber, linux, cve, linuxSec, deep, lone}

	assign := func(title string, minutes int, tags ...*pb.Tag) {
		id := insertItem(t, database, src.Id, title, "", minutes)
		for _, tg := range tags {
			if err := db.AssignTagToItem(database, id, tg.Id); err != nil {
				t.Fatal(err)
			}
		}
	}
	assign("cve", 0, cve)
	assign("linsec", 1, linuxSec)
	assign("kernel", 2, deep)
	assign("both", 3, cve, cyber)
	assign("lone", 4, lone)
	assign("untagged", 5)

	check := func(label string) {
		t.Helper()
		items, _, err := db.ListItems(database, db.ItemFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		for _, tg := range all {
			viaHub := []string{}
			for _, it := range items {
				if svc.itemMatchesFilter(ItemToProto(it), &pb.StreamFilter{TagId: tg.Id}) {
					viaHub = append(viaHub, it.Title)
				}
			}
			listed, _, err := db.ListItems(database, db.ItemFilter{TagID: tg.Id, Limit: 1000})
			if err != nil {
				t.Fatal(err)
			}
			viaList := make([]string, len(listed))
			for i, it := range listed {
				viaList[i] = it.Title
			}
			sort.Strings(viaHub)
			sort.Strings(viaList)
			if !reflect.DeepEqual(viaHub, viaList) {
				t.Errorf("%s, tag %q: hub %v, ListItems %v", label, tg.Name, viaHub, viaList)
			}
		}
	}
	check("initial")

	// Move linux security under lone only; the graph snapshot follows.
	if _, err := svc.UpdateTag(ctx, &pb.UpdateTagRequest{Id: linuxSec.Id, Parents: &pb.TagParents{Ids: []int64{lone.Id}}}); err != nil {
		t.Fatal(err)
	}
	check("after UpdateTag")

	// Delete a parent: its children stay.
	if _, err := svc.RemoveTag(ctx, &pb.RemoveTagRequest{Id: lone.Id}); err != nil {
		t.Fatal(err)
	}
	all = all[:5]
	check("after RemoveTag")
}
