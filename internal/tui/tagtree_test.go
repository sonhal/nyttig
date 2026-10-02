package tui

import (
	"reflect"
	"strings"
	"testing"
)

func tg(id int64, name string, parents ...int64) TagInfo {
	return TagInfo{ID: id, Name: name, ParentIDs: parents}
}

// flatNames renders the flattened order as "depth:name".
func flatNames(tags []TagInfo) []string {
	var out []string
	for _, t := range tags {
		out = append(out, string(rune('0'+t.Depth))+":"+t.Name)
	}
	return out
}

func TestFlattenTagTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []TagInfo
		want []string
	}{
		{"empty", nil, nil},
		{"flat keeps order", []TagInfo{tg(1, "b"), tg(2, "a")}, []string{"0:b", "0:a"}},
		{
			"children follow their parent",
			[]TagInfo{tg(1, "a"), tg(2, "b"), tg(3, "a1", 1), tg(4, "b1", 2), tg(5, "a2", 1)},
			[]string{"0:a", "1:a1", "1:a2", "0:b", "1:b1"},
		},
		{
			"child listed before its parent",
			[]TagInfo{tg(2, "child", 1), tg(1, "parent")},
			[]string{"0:parent", "1:child"},
		},
		{
			"diamond shows the shared tag once, at its first position",
			[]TagInfo{tg(1, "cyber security"), tg(2, "linux"), tg(3, "CVE", 1), tg(4, "linux security", 1, 2)},
			[]string{"0:cyber security", "1:CVE", "1:linux security", "0:linux"},
		},
		{
			"deep chain",
			[]TagInfo{tg(1, "a"), tg(2, "b", 1), tg(3, "c", 2), tg(4, "d", 3)},
			[]string{"0:a", "1:b", "2:c", "3:d"},
		},
		{"unknown parent counts as a root", []TagInfo{tg(1, "a", 99)}, []string{"0:a"}},
		{"a cycle loses no tag", []TagInfo{tg(1, "a", 2), tg(2, "b", 1)}, []string{"0:a", "1:b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := flatNames(flattenTagTree(tc.in)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTagDescendantCounts(t *testing.T) {
	tags := []TagInfo{tg(1, "cyber security"), tg(2, "linux"), tg(3, "CVE", 1), tg(4, "linux security", 1, 2), tg(5, "kernel", 4)}
	got := tagDescendantCounts(tags)
	want := map[int64]int{1: 3, 2: 2, 3: 0, 4: 1, 5: 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilterBar_TagDropdownOrderAndLabel(t *testing.T) {
	f := NewFilterBar()
	f.SetWidth(200)
	f.SetTags([]TagInfo{tg(1, "cyber security"), tg(2, "CVE", 1), tg(3, "linux security", 1), tg(4, "alone")})

	var order []string
	for i := 0; i < 5; i++ {
		order = append(order, f.tags[f.tagIdx].Name)
		f.CycleTag()
	}
	if want := []string{"all", "cyber security", "CVE", "linux security", "alone"}; !reflect.DeepEqual(order, want) {
		t.Errorf("cycle order = %v, want %v", order, want)
	}
	if f.CurrentTagID() != 0 {
		t.Errorf("cycling wraps back to all")
	}

	f.CycleTag() // cyber security
	if view := f.View(); !strings.Contains(view, "cyber security +2") {
		t.Errorf("label missing the +2 count: %q", view)
	}
	f.CycleTag() // CVE, no children
	if view := f.View(); strings.Contains(view, "+") {
		t.Errorf("leaf tag label has a count: %q", view)
	}
}
