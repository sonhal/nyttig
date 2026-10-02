package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

func TestValidateViewFilter(t *testing.T) {
	tests := []struct {
		name    string
		f       *pb.ViewFilter
		wantErr string
	}{
		{"nil", nil, ""},
		{"empty", &pb.ViewFilter{}, ""},
		{"full", &pb.ViewFilter{Search: "go", SourceId: 1, TagId: 2, Sort: "oldest", UnviewedOnly: true}, ""},
		{"newest", &pb.ViewFilter{Sort: "newest"}, ""},
		{"bad sort", &pb.ViewFilter{Sort: "random"}, "sort"},
		{"search too long", &pb.ViewFilter{Search: strings.Repeat("a", maxViewSearchLen+1)}, "search"},
		{"search at limit", &pb.ViewFilter{Search: strings.Repeat("é", maxViewSearchLen)}, ""},
		{"control char", &pb.ViewFilter{Search: "a\nb"}, "control"},
		{"negative source", &pb.ViewFilter{SourceId: -1}, "source_id"},
		{"negative tag", &pb.ViewFilter{TagId: -1}, "tag_id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateViewFilter(tc.f)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestAddSavedView_Validation(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	for name, req := range map[string]*pb.AddSavedViewRequest{
		"empty name":   {Name: "  "},
		"long name":    {Name: strings.Repeat("n", maxViewNameLen+1)},
		"control name": {Name: "a\tb"},
		"bad sort":     {Name: "v", Filter: &pb.ViewFilter{Sort: "x"}},
		"long search":  {Name: "v", Filter: &pb.ViewFilter{Search: strings.Repeat("a", maxViewSearchLen+1)}},
	} {
		_, err := svc.AddSavedView(ctx, req)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}
	// Nothing was stored.
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	if len(list.Views) != 0 {
		t.Fatalf("%d views stored after rejected adds", len(list.Views))
	}
}

func TestSavedViews_AddListUpdateRemove(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	tag, err := svc.AddTag(ctx, &pb.AddTagRequest{Name: "rust"})
	if err != nil {
		t.Fatal(err)
	}

	v, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{
		Name:     "Rust",
		Filter:   &pb.ViewFilter{Search: "async", SourceId: src.Id, TagId: tag.Id, Sort: "oldest", UnviewedOnly: true},
		Favorite: true,
	})
	if err != nil {
		t.Fatalf("AddSavedView: %v", err)
	}
	if v.Id == 0 || v.Name != "Rust" || !v.Favorite || v.Filter.Search != "async" ||
		v.Filter.SourceId != src.Id || v.Filter.TagId != tag.Id || v.Filter.Sort != "oldest" || !v.Filter.UnviewedOnly {
		t.Fatalf("unexpected view %v", v)
	}

	// No filter at all: sort defaults to newest.
	plain, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Filter.Sort != "newest" || plain.Position <= v.Position {
		t.Fatalf("plain view = %v (first at position %d)", plain, v.Position)
	}

	// Rename only: filter and favorite stay.
	renamed, err := svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Name: proto.String("Rust news")})
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Rust news" || !renamed.Favorite || renamed.Filter.TagId != tag.Id {
		t.Fatalf("after rename: %v", renamed)
	}
	// favorite=false (presence matters), filter untouched.
	unfav, err := svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Favorite: proto.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if unfav.Favorite || unfav.Name != "Rust news" || unfav.Filter.Search != "async" {
		t.Fatalf("after unfavorite: %v", unfav)
	}
	// A set filter replaces the whole filter.
	replaced, err := svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{Search: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	f := replaced.Filter
	if f.Search != "go" || f.SourceId != 0 || f.TagId != 0 || f.UnviewedOnly || f.Sort != "newest" {
		t.Fatalf("after filter replace: %v", f)
	}
	// An invalid update changes nothing.
	_, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Name: proto.String(""), Filter: &pb.ViewFilter{Search: "never"}})
	wantCode(t, err, codes.InvalidArgument)
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	if len(list.Views) != 2 || list.Views[0].Filter.Search != "go" || list.Views[0].Name != "Rust news" {
		t.Fatalf("list = %v", list.Views)
	}

	if _, err := svc.RemoveSavedView(ctx, &pb.RemoveSavedViewRequest{Id: v.Id}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.RemoveSavedView(ctx, &pb.RemoveSavedViewRequest{Id: v.Id})
	wantCode(t, err, codes.NotFound)
}

func TestSavedViews_NotFound(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	_, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "a", Filter: &pb.ViewFilter{SourceId: 999}})
	wantCode(t, err, codes.NotFound)
	_, err = svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "a", Filter: &pb.ViewFilter{TagId: 999}})
	wantCode(t, err, codes.NotFound)
	_, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: 999, Name: proto.String("x")})
	wantCode(t, err, codes.NotFound)
	_, err = svc.RemoveSavedView(ctx, &pb.RemoveSavedViewRequest{Id: 999})
	wantCode(t, err, codes.NotFound)

	v, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: v.Id, Filter: &pb.ViewFilter{TagId: 999}})
	wantCode(t, err, codes.NotFound)
}

func TestSavedViews_AlreadyExists(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if _, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "Security"}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "security"})
	wantCode(t, err, codes.AlreadyExists)

	other, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: other.Id, Name: proto.String("SECURITY")})
	wantCode(t, err, codes.AlreadyExists)
	// Renaming a view to its own name in another case is fine.
	if _, err := svc.UpdateSavedView(ctx, &pb.UpdateSavedViewRequest{Id: other.Id, Name: proto.String("other")}); err != nil {
		t.Fatalf("rename to own name: %v", err)
	}
}

func TestSavedViews_Cap(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	for i := 0; i < maxSavedViews; i++ {
		if _, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: fmt.Sprintf("view %d", i)}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	_, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "one too many"})
	wantCode(t, err, codes.FailedPrecondition)

	// Removing one makes room again.
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	if _, err := svc.RemoveSavedView(ctx, &pb.RemoveSavedViewRequest{Id: list.Views[0].Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "one too many"}); err != nil {
		t.Fatalf("add after remove: %v", err)
	}
}

func TestReorderSavedViews(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	var ids []int64
	for _, n := range []string{"a", "b", "c"} {
		v, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: n})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.Id)
	}
	a, b, c := ids[0], ids[1], ids[2]

	resp, err := svc.ReorderSavedViews(ctx, &pb.ReorderSavedViewsRequest{Ids: []int64{c, a, b}})
	if err != nil {
		t.Fatalf("ReorderSavedViews: %v", err)
	}
	got := []string{}
	for _, v := range resp.Views {
		got = append(got, v.Name)
	}
	if fmt.Sprint(got) != "[c a b]" {
		t.Fatalf("order = %v, want [c a b]", got)
	}
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	if list.Views[0].Id != c || list.Views[0].Position >= list.Views[1].Position {
		t.Fatalf("list after reorder = %v", list.Views)
	}

	for name, in := range map[string][]int64{
		"partial": {a, b}, "unknown": {a, b, 999}, "duplicate": {a, a, b}, "empty": nil,
	} {
		_, err := svc.ReorderSavedViews(ctx, &pb.ReorderSavedViewsRequest{Ids: in})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, codes.InvalidArgument)
	}
}

func TestSavedViews_SourceDeleteKeepsView(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	src := addSource(t, svc, "feed", "https://example.com/feed.xml")
	if _, err := svc.AddSavedView(ctx, &pb.AddSavedViewRequest{Name: "v", Filter: &pb.ViewFilter{SourceId: src.Id, Search: "q"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveSource(ctx, &pb.RemoveSourceRequest{Id: src.Id}); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.ListSavedViews(ctx, &emptypb.Empty{})
	if len(list.Views) != 1 || list.Views[0].Filter.SourceId != 0 || list.Views[0].Filter.Search != "q" {
		t.Fatalf("list = %v", list.Views)
	}
}

// TestSavedViewSurvivesWire guards against generated code drifting from the
// proto (see TestSourceColorSurvivesWire), and checks that the optional
// fields of UpdateSavedViewRequest keep their presence.
func TestSavedViewSurvivesWire(t *testing.T) {
	in := &pb.SavedView{
		Id: 7, Name: "sec", Favorite: true, Position: 3,
		Filter: &pb.ViewFilter{Search: "q", SourceId: 4, TagId: 5, Sort: "oldest", UnviewedOnly: true},
	}
	raw, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := &pb.SavedView{}
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !proto.Equal(in, out) {
		t.Fatalf("after round trip %v, want %v", out, in)
	}

	up := &pb.UpdateSavedViewRequest{Id: 7, Name: proto.String(""), Favorite: proto.Bool(false), Filter: &pb.ViewFilter{}}
	raw, err = proto.Marshal(up)
	if err != nil {
		t.Fatal(err)
	}
	got := &pb.UpdateSavedViewRequest{}
	if err := proto.Unmarshal(raw, got); err != nil {
		t.Fatal(err)
	}
	if got.Name == nil || got.Favorite == nil || got.Filter == nil {
		t.Fatalf("presence lost on the wire: %v", got)
	}
	none := &pb.UpdateSavedViewRequest{}
	raw, _ = proto.Marshal(none)
	got = &pb.UpdateSavedViewRequest{}
	_ = proto.Unmarshal(raw, got)
	if got.Name != nil || got.Favorite != nil || got.Filter != nil {
		t.Fatalf("unset fields appeared on the wire: %v", got)
	}
}
