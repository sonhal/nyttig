package service

import "github.com/sonhal/nyttig/internal/server/db"

// tagGraph is an immutable snapshot of the tag tree, parent to children.
type tagGraph struct {
	children map[int64][]int64
}

func newTagGraph(edges []db.TagEdge) *tagGraph {
	g := &tagGraph{children: make(map[int64][]int64, len(edges))}
	for _, e := range edges {
		g.children[e.ParentID] = append(g.children[e.ParentID], e.ChildID)
	}
	return g
}

// subtree returns id and every tag below it. A breadth-first walk with a
// visited set, so a tag under two parents is visited once and a cycle (which
// the db layer rejects) could not loop. It is the in-memory twin of
// db.SubtreeSQL.
func (g *tagGraph) subtree(id int64) map[int64]bool {
	seen := map[int64]bool{id: true}
	queue := []int64{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range g.children[cur] {
			if !seen[c] {
				seen[c] = true
				queue = append(queue, c)
			}
		}
	}
	return seen
}
