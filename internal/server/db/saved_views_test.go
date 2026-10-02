package db

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func addView(t *testing.T, database *sql.DB, v *SavedView) int64 {
	t.Helper()
	id, err := InsertSavedView(database, v)
	if err != nil {
		t.Fatalf("InsertSavedView(%q): %v", v.Name, err)
	}
	return id
}

func viewIDs(t *testing.T, database *sql.DB) []int64 {
	t.Helper()
	views, err := ListSavedViews(database)
	if err != nil {
		t.Fatalf("ListSavedViews: %v", err)
	}
	ids := []int64{}
	for _, v := range views {
		ids = append(ids, v.ID)
	}
	return ids
}

func TestSavedViews_CRUD(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	tag := mustTag(t, database, "rust")

	id := addView(t, database, &SavedView{
		Name: "Rust", Search: "async", SourceID: &src, TagID: &tag,
		Sort: "oldest", UnviewedOnly: true, Favorite: true,
	})
	got, err := GetSavedView(database, id)
	if err != nil || got == nil {
		t.Fatalf("GetSavedView = %v, %v", got, err)
	}
	want := &SavedView{ID: id, Name: "Rust", Search: "async", SourceID: &src, TagID: &tag,
		Sort: "oldest", UnviewedOnly: true, Favorite: true, Position: 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	// An empty sort stores the default, nil ids stay NULL.
	id2 := addView(t, database, &SavedView{Name: "plain"})
	plain, _ := GetSavedView(database, id2)
	if plain.Sort != "newest" || plain.SourceID != nil || plain.TagID != nil || plain.Favorite {
		t.Fatalf("plain view = %+v", plain)
	}

	got.Name = "Rust news"
	got.SourceID = nil
	got.Sort = "newest"
	got.Favorite = false
	ok, err := UpdateSavedView(database, got)
	if err != nil || !ok {
		t.Fatalf("UpdateSavedView = %v, %v", ok, err)
	}
	again, _ := GetSavedView(database, id)
	if again.Name != "Rust news" || again.SourceID != nil || again.TagID == nil || again.Sort != "newest" || again.Favorite {
		t.Fatalf("after update: %+v", again)
	}
	if again.Position != 0 {
		t.Errorf("update changed position to %d", again.Position)
	}

	missing := &SavedView{ID: 999, Name: "x"}
	if ok, err := UpdateSavedView(database, missing); err != nil || ok {
		t.Errorf("update of missing view = %v, %v; want false, nil", ok, err)
	}
	if v, err := GetSavedView(database, 999); err != nil || v != nil {
		t.Errorf("GetSavedView(missing) = %v, %v; want nil, nil", v, err)
	}

	if n, _ := CountSavedViews(database); n != 2 {
		t.Errorf("CountSavedViews = %d, want 2", n)
	}
	if ok, err := DeleteSavedView(database, id); err != nil || !ok {
		t.Fatalf("DeleteSavedView = %v, %v", ok, err)
	}
	if ok, err := DeleteSavedView(database, id); err != nil || ok {
		t.Fatalf("second DeleteSavedView = %v, %v; want false, nil", ok, err)
	}
}

func TestSavedViews_NameUniqueNoCase(t *testing.T) {
	database := openTestDB(t)
	addView(t, database, &SavedView{Name: "Security"})
	_, err := InsertSavedView(database, &SavedView{Name: "SECURITY"})
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate name error = %v, want a UNIQUE violation", err)
	}

	other := addView(t, database, &SavedView{Name: "Other"})
	ok, err := UpdateSavedView(database, &SavedView{ID: other, Name: "security"})
	if ok || err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("rename to a taken name = %v, %v; want a UNIQUE violation", ok, err)
	}
}

func TestSavedViews_AppendedAtEnd(t *testing.T) {
	database := openTestDB(t)
	a := addView(t, database, &SavedView{Name: "a"})
	b := addView(t, database, &SavedView{Name: "b"})
	c := addView(t, database, &SavedView{Name: "c"})
	if got := viewIDs(t, database); !reflect.DeepEqual(got, []int64{a, b, c}) {
		t.Fatalf("order = %v", got)
	}
	// After deleting one, a new view still goes last.
	if _, err := DeleteSavedView(database, b); err != nil {
		t.Fatal(err)
	}
	d := addView(t, database, &SavedView{Name: "d"})
	if got := viewIDs(t, database); !reflect.DeepEqual(got, []int64{a, c, d}) {
		t.Fatalf("order = %v", got)
	}
	views, _ := ListSavedViews(database)
	if views[2].Position <= views[1].Position {
		t.Errorf("positions not increasing: %d, %d", views[1].Position, views[2].Position)
	}
}

func TestReorderSavedViews(t *testing.T) {
	database := openTestDB(t)
	a := addView(t, database, &SavedView{Name: "a"})
	b := addView(t, database, &SavedView{Name: "b"})
	c := addView(t, database, &SavedView{Name: "c"})

	if err := ReorderSavedViews(database, []int64{c, a, b}); err != nil {
		t.Fatalf("ReorderSavedViews: %v", err)
	}
	if got := viewIDs(t, database); !reflect.DeepEqual(got, []int64{c, a, b}) {
		t.Fatalf("order = %v, want %v", got, []int64{c, a, b})
	}

	for name, ids := range map[string][]int64{
		"partial":   {a, b},
		"unknown":   {a, b, 999},
		"duplicate": {a, a, b},
		"extra":     {a, b, c, 999},
		"empty":     {},
	} {
		err := ReorderSavedViews(database, ids)
		if !errors.Is(err, ErrViewOrder) {
			t.Errorf("%s: err = %v, want ErrViewOrder", name, err)
		}
	}
	// Rejected calls changed nothing.
	if got := viewIDs(t, database); !reflect.DeepEqual(got, []int64{c, a, b}) {
		t.Fatalf("order after rejects = %v", got)
	}
}

func TestSavedViews_DeleteSourceOrTagKeepsView(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	tag := mustTag(t, database, "rust")
	id := addView(t, database, &SavedView{Name: "v", Search: "q", SourceID: &src, TagID: &tag, UnviewedOnly: true})

	if n, _ := CountViewsUsingSource(database, src); n != 1 {
		t.Errorf("CountViewsUsingSource = %d, want 1", n)
	}
	if n, _ := CountViewsUsingTag(database, tag); n != 1 {
		t.Errorf("CountViewsUsingTag = %d, want 1", n)
	}

	if ok, err := DeleteTag(database, tag); err != nil || !ok {
		t.Fatalf("DeleteTag = %v, %v", ok, err)
	}
	v, _ := GetSavedView(database, id)
	if v == nil || v.TagID != nil || v.SourceID == nil || v.Search != "q" {
		t.Fatalf("after tag delete: %+v", v)
	}

	if ok, err := DeleteSource(database, src); err != nil || !ok {
		t.Fatalf("DeleteSource = %v, %v", ok, err)
	}
	v, _ = GetSavedView(database, id)
	if v == nil || v.SourceID != nil || !v.UnviewedOnly {
		t.Fatalf("after source delete: %+v", v)
	}
	if n, _ := CountViewsUsingSource(database, src); n != 0 {
		t.Errorf("CountViewsUsingSource after delete = %d, want 0", n)
	}
}

func TestSavedViews_SortCheck(t *testing.T) {
	database := openTestDB(t)
	if _, err := InsertSavedView(database, &SavedView{Name: "bad", Sort: "random"}); err == nil {
		t.Fatal("an unknown sort was accepted")
	}
}
