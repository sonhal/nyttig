package db

import (
	"database/sql"
	"reflect"
	"testing"
)

// retagItem inserts an item and returns its ID.
func retagItem(t *testing.T, database *sql.DB, src int64, title string, desc *string) int64 {
	t.Helper()
	id, _, err := InsertItem(database, &Item{SourceID: src, GUID: title, Link: "http://example.com/" + title, Title: title, Description: desc})
	if err != nil {
		t.Fatalf("InsertItem(%q): %v", title, err)
	}
	return id
}

func TestRetagHelpers(t *testing.T) {
	database := openTestDB(t)
	srcA := testSource(t, database, "a")
	srcB := testSource(t, database, "b")
	rust := mustTag(t, database, "rust")
	golang := mustTag(t, database, "golang")
	desc := "cargo"
	a1 := retagItem(t, database, srcA, "a1", &desc)
	a2 := retagItem(t, database, srcA, "a2", nil)
	b1 := retagItem(t, database, srcB, "b1", nil)

	ruleID, err := InsertTagRule(database, &TagRule{TagID: rust, Field: "both", Pattern: "rust"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InsertTagRule(database, &TagRule{TagID: golang, SourceID: &srcB, Field: "title", Pattern: "go"}); err != nil {
		t.Fatal(err)
	}

	t.Run("exists", func(t *testing.T) {
		for _, c := range []struct {
			name string
			fn   func(Querier, int64) (bool, error)
			id   int64
			want bool
		}{
			{"tag", TagExists, rust, true},
			{"missing tag", TagExists, 9999, false},
			{"source", SourceExists, srcB, true},
			{"missing source", SourceExists, 9999, false},
		} {
			got, err := c.fn(database, c.id)
			if err != nil || got != c.want {
				t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
			}
		}
	})

	t.Run("tag names", func(t *testing.T) {
		names, err := ListTagNames(database)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64]string{rust: "rust", golang: "golang"}
		if !reflect.DeepEqual(names, want) {
			t.Errorf("ListTagNames = %v, want %v", names, want)
		}
	})

	t.Run("rules", func(t *testing.T) {
		all, err := ListTagRulesForTag(database, 0)
		if err != nil || len(all) != 2 {
			t.Fatalf("all rules: %d, %v", len(all), err)
		}
		one, err := ListTagRulesForTag(database, rust)
		if err != nil || len(one) != 1 || one[0].ID != ruleID || one[0].SourceID != nil || one[0].TagName != "rust" {
			t.Fatalf("rust rules: %+v, %v", one, err)
		}
		other, err := ListTagRulesForTag(database, golang)
		if err != nil || len(other) != 1 || other[0].SourceID == nil || *other[0].SourceID != srcB {
			t.Fatalf("golang rules: %+v, %v", other, err)
		}
	})

	t.Run("items", func(t *testing.T) {
		all, err := ListItemsForTagging(database, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := []TaggableItem{
			{ID: a1, SourceID: srcA, Title: "a1", Description: "cargo"},
			{ID: a2, SourceID: srcA, Title: "a2"},
			{ID: b1, SourceID: srcB, Title: "b1"},
		}
		if !reflect.DeepEqual(all, want) {
			t.Errorf("all items = %+v, want %+v", all, want)
		}
		onlyB, err := ListItemsForTagging(database, srcB)
		if err != nil || !reflect.DeepEqual(onlyB, want[2:]) {
			t.Errorf("source b items = %+v, %v", onlyB, err)
		}
	})

	t.Run("assign, list, remove", func(t *testing.T) {
		pairs := []ItemTag{{a1, rust}, {a2, rust}, {b1, rust}, {b1, golang}}
		// Twice: inserting an existing row is ignored.
		for range 2 {
			if err := AssignTagsToItems(database, pairs); err != nil {
				t.Fatal(err)
			}
		}
		check := func(tag, src int64, want []ItemTag) {
			t.Helper()
			got, err := ListItemTagPairs(database, tag, src)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ListItemTagPairs(%d, %d) = %v, want %v", tag, src, got, want)
			}
		}
		check(0, 0, pairs)
		check(rust, 0, pairs[:3])
		check(0, srcB, pairs[2:])
		check(rust, srcA, pairs[:2])

		if err := RemoveTagsFromItems(database, []ItemTag{{a2, rust}, {b1, golang}, {a2, golang}}); err != nil {
			t.Fatal(err)
		}
		// Source a's rows other than a2's are untouched, and so is source b's rust row.
		check(0, 0, []ItemTag{{a1, rust}, {b1, rust}})
	})

	t.Run("in a transaction", func(t *testing.T) {
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := AssignTagsToItems(tx, []ItemTag{{a2, golang}}); err != nil {
			t.Fatal(err)
		}
		if got, _ := ListItemTagPairs(tx, golang, 0); len(got) != 1 {
			t.Errorf("inside the transaction: %v", got)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if got, _ := ListItemTagPairs(database, golang, 0); len(got) != 0 {
			t.Errorf("after rollback: %v", got)
		}
	})
}
