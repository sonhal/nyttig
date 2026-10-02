package tui

// flattenTagTree orders tags depth-first from the roots (tags with no parent
// in tags), siblings in the order given, and sets each tag's Depth. A tag with
// several parents appears once, at its first position. Tags left over (only
// possible in a cycle, which the daemon rejects) follow as roots so none is
// lost.
func flattenTagTree(tags []TagInfo) []TagInfo {
	known := make(map[int64]bool, len(tags))
	for _, t := range tags {
		known[t.ID] = true
	}
	children := childrenByParent(tags)

	out := make([]TagInfo, 0, len(tags))
	seen := make(map[int64]bool, len(tags))
	var walk func(t TagInfo, depth int)
	walk = func(t TagInfo, depth int) {
		if seen[t.ID] {
			return
		}
		seen[t.ID] = true
		t.Depth = depth
		out = append(out, t)
		for _, c := range children[t.ID] {
			walk(c, depth+1)
		}
	}
	for _, t := range tags {
		if !hasKnownParent(t, known) {
			walk(t, 0)
		}
	}
	for _, t := range tags {
		walk(t, 0)
	}
	return out
}

// tagDescendantCounts returns, for each tag ID, how many distinct tags sit
// below it.
func tagDescendantCounts(tags []TagInfo) map[int64]int {
	children := childrenByParent(tags)
	counts := make(map[int64]int, len(tags))
	for _, t := range tags {
		seen := map[int64]bool{t.ID: true}
		queue := []int64{t.ID}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, c := range children[cur] {
				if !seen[c.ID] {
					seen[c.ID] = true
					queue = append(queue, c.ID)
				}
			}
		}
		counts[t.ID] = len(seen) - 1
	}
	return counts
}

func childrenByParent(tags []TagInfo) map[int64][]TagInfo {
	children := make(map[int64][]TagInfo)
	for _, t := range tags {
		for _, p := range t.ParentIDs {
			children[p] = append(children[p], t)
		}
	}
	return children
}

func hasKnownParent(t TagInfo, known map[int64]bool) bool {
	for _, p := range t.ParentIDs {
		if known[p] {
			return true
		}
	}
	return false
}
