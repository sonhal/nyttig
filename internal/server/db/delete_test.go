package db

import "testing"

func TestDelete_ReportsWhetherARowWasRemoved(t *testing.T) {
	database := openTestDB(t)
	src := testSource(t, database, "feed")
	tagID, err := InsertTag(database, "rust", nil)
	if err != nil {
		t.Fatalf("InsertTag: %v", err)
	}
	ruleID, err := InsertTagRule(database, &TagRule{TagID: tagID, Field: "both", Pattern: "x"})
	if err != nil {
		t.Fatalf("InsertTagRule: %v", err)
	}

	for _, tc := range []struct {
		name string
		del  func(id int64) (bool, error)
		id   int64
	}{
		{"rule", func(id int64) (bool, error) { return DeleteTagRule(database, id) }, ruleID},
		{"tag", func(id int64) (bool, error) { return DeleteTag(database, id) }, tagID},
		{"source", func(id int64) (bool, error) { return DeleteSource(database, id) }, src},
	} {
		if ok, err := tc.del(tc.id + 1000); err != nil || ok {
			t.Errorf("%s: delete of unknown id = %v, %v; want false, nil", tc.name, ok, err)
		}
		if ok, err := tc.del(tc.id); err != nil || !ok {
			t.Errorf("%s: delete of existing id = %v, %v; want true, nil", tc.name, ok, err)
		}
		if ok, err := tc.del(tc.id); err != nil || ok {
			t.Errorf("%s: second delete = %v, %v; want false, nil", tc.name, ok, err)
		}
	}
}
