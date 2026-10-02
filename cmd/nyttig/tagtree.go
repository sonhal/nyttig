package main

import (
	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
)

// tagRow is one line of the list-tags tree.
type tagRow struct {
	Tag       *pb.Tag
	Depth     int
	AlsoUnder []string // the tag's other parents, for a tag with several
}

// tagTreeRows orders tags depth-first from the roots, siblings in the order
// given (ListTags sorts by name). A tag with several parents appears under
// each of them, marked with the others in AlsoUnder. A tag whose parents are
// all missing from tags counts as a root, and a cycle (which the daemon
// rejects) is cut where it closes, so the walk always ends.
func tagTreeRows(tags []*pb.Tag) []tagRow {
	byID := make(map[int64]*pb.Tag, len(tags))
	for _, t := range tags {
		byID[t.Id] = t
	}
	children := make(map[int64][]*pb.Tag)
	var roots []*pb.Tag
	for _, t := range tags {
		var known []int64
		for _, p := range t.ParentIds {
			if byID[p] != nil {
				known = append(known, p)
				children[p] = append(children[p], t)
			}
		}
		if len(known) == 0 {
			roots = append(roots, t)
		}
	}

	var rows []tagRow
	onPath := map[int64]bool{}
	var walk func(t *pb.Tag, parent int64, depth int)
	walk = func(t *pb.Tag, parent int64, depth int) {
		if onPath[t.Id] {
			return
		}
		row := tagRow{Tag: t, Depth: depth}
		for _, p := range t.ParentIds {
			if p != parent && byID[p] != nil {
				row.AlsoUnder = append(row.AlsoUnder, byID[p].Name)
			}
		}
		rows = append(rows, row)
		onPath[t.Id] = true
		for _, c := range children[t.Id] {
			walk(c, t.Id, depth+1)
		}
		onPath[t.Id] = false
	}
	for _, r := range roots {
		walk(r, 0, 0)
	}
	return rows
}
