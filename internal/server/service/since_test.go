package service

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

// TestSinceSurvivesWire guards the generated code like
// TestSourceColorSurvivesWire: a field missing from the embedded descriptor
// is silently dropped.
func TestSinceSurvivesWire(t *testing.T) {
	cutoff := timestamppb.New(time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	for _, in := range []proto.Message{
		&pb.SavedView{Id: 1, Name: "week", Filter: &pb.ViewFilter{Since: "7d"}},
		&pb.SearchRequest{Query: "x", After: cutoff},
		&pb.StreamFilter{Search: "x", After: cutoff},
	} {
		raw, err := proto.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		out := in.ProtoReflect().New().Interface()
		if err := proto.Unmarshal(raw, out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !proto.Equal(in, out) {
			t.Errorf("%T: after round trip %v, want %v", in, out, in)
		}
	}
}

func TestSavedView_SinceRoundTrip(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	v, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "week", Filter: &pb.ViewFilter{Since: "7d", Search: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Filter.Since != "7d" {
		t.Fatalf("added since = %q", v.Filter.Since)
	}
	list, err := svc.ListSavedViews(ctx, nil)
	if err != nil || len(list.Views) != 1 || list.Views[0].Filter.Since != "7d" {
		t.Fatalf("list = %v, %v", list, err)
	}
	// Replacing the filter replaces the window; a filter without it clears it.
	v, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{Since: "1mo"}})
	if err != nil || v.Filter.Since != "1mo" {
		t.Fatalf("update = %v, %v", v, err)
	}
	v, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{}})
	if err != nil || v.Filter.Since != "" {
		t.Fatalf("clear = %v, %v", v, err)
	}
	// An invalid window is rejected on add and update, and changes nothing.
	_, err = svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "bad", Filter: &pb.ViewFilter{Since: "1m"}})
	wantCode(t, err, codes.InvalidArgument)
	_, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{Since: "soon"}})
	wantCode(t, err, codes.InvalidArgument)
	got, _ := svc.ListSavedViews(ctx, nil)
	if got.Views[0].Filter.Since != "" {
		t.Errorf("since after rejected update = %q", got.Views[0].Filter.Since)
	}
}

func TestSearch_After(t *testing.T) {
	svc, database := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "early", "", 0)
	insertItem(t, database, src.Id, "at", "", 10)
	insertItem(t, database, src.Id, "late", "", 20)
	base := time.Date(2026, 9, 1, 12, 10, 0, 0, time.UTC)

	resp, err := svc.Search(ctx, &pb.SearchRequest{Sort: "oldest", After: timestamppb.New(base)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"at", "late"}; !reflect.DeepEqual(titles(resp.Items), want) || resp.Total != 2 {
		t.Errorf("items = %v total %d, want %v", titles(resp.Items), resp.Total, want)
	}
	// Fractions of a second do not move the cutoff past the whole second.
	resp, _ = svc.Search(ctx, &pb.SearchRequest{Sort: "oldest", After: timestamppb.New(base.Add(500 * time.Millisecond))})
	if want := []string{"at", "late"}; !reflect.DeepEqual(titles(resp.Items), want) {
		t.Errorf("fractional cutoff items = %v, want %v", titles(resp.Items), want)
	}
	// Unset = no window.
	resp, _ = svc.Search(ctx, &pb.SearchRequest{})
	if resp.Total != 3 {
		t.Errorf("no window total = %d", resp.Total)
	}
	// Out of range timestamps are rejected.
	_, err = svc.Search(ctx, &pb.SearchRequest{After: &timestamppb.Timestamp{Seconds: 1 << 60}})
	wantCode(t, err, codes.InvalidArgument)
}

// TestItemMatchesFilter_AfterAgreesWithListItems compares the Hub's check
// with ListItems for items with a published date, without one, and at the
// cutoff second.
func TestItemMatchesFilter_AfterAgreesWithListItems(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	insertItem(t, database, src.Id, "old", "", -60)
	insertItem(t, database, src.Id, "before", "", -1)
	insertItem(t, database, src.Id, "at", "", 0)
	insertItem(t, database, src.Id, "after", "", 1)
	insertItem(t, database, src.Id, "future", "", 60*24*365)
	cutoff := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	// Items without a published date: the fetch time decides.
	for guid, fetched := range map[string]string{
		"nodate-old": "2026-09-01 11:59:59",
		"nodate-at":  "2026-09-01 12:00:00",
		"nodate-new": "2026-09-02 00:00:00",
	} {
		id, _, err := db.InsertItem(database, &db.Item{SourceID: src.Id, GUID: guid, Link: "l", Title: guid})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE items SET fetched_at = ? WHERE id = ?`, fetched, id); err != nil {
			t.Fatal(err)
		}
	}

	for _, after := range []time.Time{cutoff, cutoff.Add(300 * time.Millisecond), cutoff.Add(-24 * time.Hour), cutoff.Add(10 * 365 * 24 * time.Hour)} {
		items, _, err := db.ListItems(database, db.ItemFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var viaHub []string
		for _, it := range items {
			if svc.itemMatchesFilter(ItemToProto(it), &pb.StreamFilter{After: timestamppb.New(after)}) {
				viaHub = append(viaHub, it.Title)
			}
		}
		listed, _, err := db.ListItems(database, db.ItemFilter{After: after, Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var viaList []string
		for _, it := range listed {
			viaList = append(viaList, it.Title)
		}
		sort.Strings(viaHub)
		sort.Strings(viaList)
		if !reflect.DeepEqual(viaHub, viaList) {
			t.Errorf("after %s: hub %v, ListItems %v", after, viaHub, viaList)
		}
	}

	// No cutoff passes everything, as before.
	it, _ := db.GetItem(database, 1)
	if !svc.itemMatchesFilter(ItemToProto(it), &pb.StreamFilter{}) {
		t.Error("unset after must pass")
	}
}
