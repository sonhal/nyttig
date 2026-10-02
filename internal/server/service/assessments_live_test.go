package service

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
)

func fptr(f float64) *float64 { return &f }

// TestAssessmentFilters_HubAgreesWithListItems compares the Hub's in-memory
// scope rules with db.ListItems over the tag tree, whole-item assessments,
// assessments on unrelated tags, exact matching and both assessors.
func TestAssessmentFilters_HubAgreesWithListItems(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")

	cve := addTag(t, svc, "CVE")
	critical := addTag(t, svc, "critical", cve.Id)
	deep := addTag(t, svc, "kernel", critical.Id)
	linux := addTag(t, svc, "linux")
	claude := addAssessor(t, svc, "claude")
	cvss := addAssessor(t, svc, "cvss")

	type as struct {
		assessor *pb.Assessor
		tag      *pb.Tag
		score    *float64
		note     string
	}
	items := []struct {
		title string
		tags  []*pb.Tag
		as    []as
	}{
		{"cve-whole", []*pb.Tag{cve}, []as{{claude, nil, fptr(0.5), ""}}},
		{"crit-on-child", []*pb.Tag{critical}, []as{{claude, critical, fptr(0.9), ""}}},
		{"crit-on-parent", []*pb.Tag{critical}, []as{{claude, cve, fptr(0.8), ""}}},
		{"deep-on-deep", []*pb.Tag{deep}, []as{{claude, deep, fptr(0.7), ""}}},
		{"cve-and-linux", []*pb.Tag{cve, linux}, []as{{claude, linux, fptr(0.95), ""}, {claude, cve, fptr(0.3), ""}}},
		{"cve-both-tags", []*pb.Tag{cve, critical}, []as{{claude, critical, fptr(0.6), ""}}},
		{"cve-none", []*pb.Tag{cve}, nil},
		{"cve-note-only", []*pb.Tag{cve}, []as{{claude, nil, nil, "just a note"}}},
		{"cve-zero", []*pb.Tag{cve}, []as{{claude, cve, fptr(0), ""}}},
		{"linux-whole", []*pb.Tag{linux}, []as{{claude, nil, fptr(0.99), ""}}},
		{"untagged", nil, nil},
		{"other-assessor", []*pb.Tag{cve}, []as{{cvss, nil, fptr(1), ""}, {claude, linux, fptr(0.9), ""}}},
	}
	for i, it := range items {
		id := insertItem(t, database, src.Id, it.title, "", i)
		for _, tg := range it.tags {
			if err := db.AssignTagToItem(database, id, tg.Id); err != nil {
				t.Fatal(err)
			}
		}
		for _, a := range it.as {
			var tagID int64
			if a.tag != nil {
				tagID = a.tag.Id
			}
			if _, err := db.PutAssessment(database, &db.Assessment{ItemID: id, AssessorID: a.assessor.Id, TagID: tagID, Score: a.score, Note: a.note}); err != nil {
				t.Fatal(err)
			}
		}
	}

	all, _, err := db.ListItems(database, db.ItemFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	protos := make([]*pb.Item, len(all))
	for i, it := range all {
		protos[i] = ItemToProto(it)
	}

	hubTitles := func(tagID int64, exact bool, assessor int64, min *float64, unassessed int64) []string {
		out := []string{}
		for _, it := range protos {
			if tagID != 0 {
				ok := false
				if exact {
					for _, tg := range it.Tags {
						ok = ok || tg.Id == tagID
					}
				} else {
					ok = svc.itemMatchesFilter(it, &pb.StreamFilter{TagId: tagID})
				}
				if !ok {
					continue
				}
			}
			if !exact {
				// The real filter path.
				if !svc.itemMatchesFilter(it, &pb.StreamFilter{TagId: tagID, AssessorId: assessor, MinScore: min, UnassessedBy: unassessed}) {
					continue
				}
			} else if !svc.assessmentsMatch(it, assessor, min, unassessed, tagID, true) {
				continue
			}
			out = append(out, it.Title)
		}
		sort.Strings(out)
		return out
	}

	n := 0
	for _, tagID := range []int64{0, cve.Id, critical.Id, deep.Id, linux.Id} {
		for _, exact := range []bool{false, true} {
			if exact && tagID == 0 {
				continue
			}
			for _, assessor := range []*pb.Assessor{claude, cvss} {
				for _, min := range []*float64{nil, fptr(0), fptr(0.5), fptr(0.7), fptr(1)} {
					for _, unassessed := range []int64{0, claude.Id, cvss.Id} {
						n++
						listed, _, err := db.ListItems(database, db.ItemFilter{
							TagID: tagID, TagExact: exact, AssessorID: assessor.Id, MinScore: min,
							UnassessedBy: unassessed, Limit: 1000,
						})
						if err != nil {
							t.Fatal(err)
						}
						want := make([]string, len(listed))
						for i, it := range listed {
							want[i] = it.Title
						}
						sort.Strings(want)
						got := hubTitles(tagID, exact, assessor.Id, min, unassessed)
						if !reflect.DeepEqual(got, want) {
							m := "nil"
							if min != nil {
								m = fmt.Sprint(*min)
							}
							t.Errorf("tag %d exact=%v assessor=%s min=%s unassessed=%d:\n hub  %v\n list %v",
								tagID, exact, assessor.Name, m, unassessed, got, want)
						}
					}
				}
			}
		}
	}
	if n < 100 {
		t.Fatalf("only %d combinations checked", n)
	}
}

// openStreamWith starts StreamItems with a filter and returns the stream
// after its first Complete, plus the titles of the initial batch.
func openStreamWith(t *testing.T, ctx context.Context, client pb.NyttigClient, f *pb.StreamFilter) (pb.Nyttig_StreamItemsClient, []string) {
	t.Helper()
	stream, err := client.StreamItems(ctx)
	if err != nil {
		t.Fatalf("StreamItems: %v", err)
	}
	if err := stream.Send(&pb.ClientMessage{Msg: &pb.ClientMessage_Filter{Filter: f}}); err != nil {
		t.Fatalf("send filter: %v", err)
	}
	var got []string
	for {
		msg, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv before Complete: %v", err)
		}
		if it := msg.GetItem(); it != nil {
			got = append(got, it.Title)
		}
		if msg.GetComplete() != nil {
			return stream, got
		}
	}
}

func recvUpdate(t *testing.T, stream pb.Nyttig_StreamItemsClient) *pb.ServerMessage {
	t.Helper()
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if msg.GetItemUpdate() == nil {
		t.Fatalf("message = %v, want an item_update", msg)
	}
	return msg
}

func TestHub_AssessmentUpdates(t *testing.T) {
	svc, database := newTestService(t)
	client, _ := startGRPC(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rpcCtx := context.Background()

	src := addSource(t, svc, "feed", "https://example.com/feed")
	shown := insertItem(t, database, src.Id, "shown", "", 3)
	later := insertItem(t, database, src.Id, "later", "", 2)
	never := insertItem(t, database, src.Id, "never", "", 1)
	claude := addAssessor(t, svc, "claude")
	put := func(item int64, score float64) {
		t.Helper()
		if _, err := svc.PutAssessment(rpcCtx, &pb.PutAssessmentRequest{ItemId: item, AssessorId: claude.Id, Score: &score}); err != nil {
			t.Fatal(err)
		}
	}
	put(shown, 0.9)

	stream, initial := openStreamWith(t, ctx, client, &pb.StreamFilter{AssessorId: claude.Id, MinScore: fptr(0.5), Sort: "score"})
	if !reflect.DeepEqual(initial, []string{"shown"}) {
		t.Fatalf("initial batch = %v, want [shown]", initial)
	}

	// An update for an item the stream shows: sent, matching, with all
	// assessments.
	put(shown, 0.95)
	msg := recvUpdate(t, stream)
	up := msg.GetItemUpdate()
	if up.Id != shown || !msg.UpdateMatches || len(up.Assessments) != 1 || *up.Assessments[0].Score != 0.95 || up.Assessments[0].AssessorName != "claude" {
		t.Fatalf("update for a shown item = %v (matches %v)", up, msg.UpdateMatches)
	}

	// An item that starts matching once it is scored.
	put(later, 0.8)
	msg = recvUpdate(t, stream)
	if msg.GetItemUpdate().Id != later || !msg.UpdateMatches {
		t.Fatalf("update for a new match = %v (matches %v)", msg.GetItemUpdate(), msg.UpdateMatches)
	}

	// An update for an item that does not match is still sent, flagged.
	put(never, 0.1)
	msg = recvUpdate(t, stream)
	if msg.GetItemUpdate().Id != never || msg.UpdateMatches {
		t.Fatalf("update for a non-match = %v (matches %v)", msg.GetItemUpdate(), msg.UpdateMatches)
	}

	// Lowering a shown item's score: sent, no longer matching. It is the
	// client that keeps showing it.
	put(shown, 0.2)
	msg = recvUpdate(t, stream)
	if msg.GetItemUpdate().Id != shown || msg.UpdateMatches {
		t.Fatalf("update after lowering = %v (matches %v)", msg.GetItemUpdate(), msg.UpdateMatches)
	}

	// Removing an assessment is an update with the remaining ones.
	if _, err := svc.RemoveAssessment(rpcCtx, &pb.RemoveAssessmentRequest{ItemId: later, AssessorId: claude.Id}); err != nil {
		t.Fatal(err)
	}
	msg = recvUpdate(t, stream)
	if msg.GetItemUpdate().Id != later || len(msg.GetItemUpdate().Assessments) != 0 || msg.UpdateMatches {
		t.Fatalf("update after remove = %v (matches %v)", msg.GetItemUpdate(), msg.UpdateMatches)
	}

	// A plain stream (no assessment filter) matches every update.
	plain, _ := openStreamWith(t, ctx, client, &pb.StreamFilter{})
	put(never, 0.4)
	if msg := recvUpdate(t, plain); !msg.UpdateMatches {
		t.Errorf("unfiltered stream: update did not match")
	}
	// Another message still arrives on the first stream.
	recvUpdate(t, stream)
}

// New items arrive without assessments, so under min_score they don't match.
func TestHub_NewItemsUnderMinScore(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	claude := addAssessor(t, svc, "claude")
	id := insertItem(t, database, src.Id, "fresh", "", 0)
	item, err := db.GetItem(database, id)
	if err != nil {
		t.Fatal(err)
	}
	p := ItemToProto(item)
	if svc.itemMatchesFilter(p, &pb.StreamFilter{AssessorId: claude.Id, MinScore: fptr(0)}) {
		t.Error("an unscored item matched min_score")
	}
	if !svc.itemMatchesFilter(p, &pb.StreamFilter{AssessorId: claude.Id, UnassessedBy: 0}) {
		t.Error("assessor_id alone filtered the item")
	}
	if !svc.itemMatchesFilter(p, &pb.StreamFilter{UnassessedBy: claude.Id}) {
		t.Error("a new item did not match unassessed_by")
	}
	if !svc.itemMatchesFilter(p, &pb.StreamFilter{AssessorId: claude.Id, Sort: "score"}) {
		t.Error("sort score filtered the item")
	}
}

// TestAssessmentFilters_WithWindowAgree checks the date window and the
// assessment filters together: the Hub's check (which decides update_matches)
// must agree with ListItems, so an assessed item that lies outside the window
// is never inserted by a live update.
func TestAssessmentFilters_WithWindowAgree(t *testing.T) {
	svc, database := newTestService(t)
	src := addSource(t, svc, "feed", "https://example.com/feed")
	cve := addTag(t, svc, "CVE")
	claude := addAssessor(t, svc, "claude")

	// minutes relative to 2026-09-01 12:00 UTC; the cutoff is that time.
	cutoff := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, it := range []struct {
		title   string
		minutes int
		score   *float64
	}{
		{"old-unassessed", -120, nil},
		{"old-scored", -60, fptr(0.9)},
		{"new-unassessed", 0, nil},
		{"new-scored-high", 1, fptr(0.9)},
		{"new-scored-low", 2, fptr(0.1)},
	} {
		id := insertItem(t, database, src.Id, it.title, "", it.minutes)
		if err := db.AssignTagToItem(database, id, cve.Id); err != nil {
			t.Fatal(err)
		}
		if it.score != nil {
			if _, err := db.PutAssessment(database, &db.Assessment{ItemID: id, AssessorID: claude.Id, Score: it.score}); err != nil {
				t.Fatal(err)
			}
		}
	}
	all, _, err := db.ListItems(database, db.ItemFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, after := range []time.Time{{}, cutoff, cutoff.Add(90 * time.Minute)} {
		for _, min := range []*float64{nil, fptr(0.5)} {
			for _, unassessed := range []int64{0, claude.Id} {
				if min != nil && unassessed != 0 {
					continue
				}
				df := db.ItemFilter{TagID: cve.Id, AssessorID: claude.Id, MinScore: min, UnassessedBy: unassessed, After: after, Limit: 1000}
				sf := &pb.StreamFilter{TagId: cve.Id, AssessorId: claude.Id, MinScore: min, UnassessedBy: unassessed}
				if !after.IsZero() {
					sf.After = timestamppb.New(after)
				}
				listed, _, err := db.ListItems(database, df)
				if err != nil {
					t.Fatal(err)
				}
				var want, got []string
				for _, it := range listed {
					want = append(want, it.Title)
				}
				for _, it := range all {
					if svc.itemMatchesFilter(ItemToProto(it), sf) {
						got = append(got, it.Title)
					}
				}
				sort.Strings(want)
				sort.Strings(got)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("after=%v min=%v unassessed=%d:\n hub  %v\n list %v", after, min, unassessed, got, want)
				}
			}
		}
	}
}
