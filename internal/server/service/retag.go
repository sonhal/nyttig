package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sonhal/nyttig/internal/proto/nyttig/v1"
	"github.com/sonhal/nyttig/internal/server/db"
	"github.com/sonhal/nyttig/internal/server/tagger"
)

// ── Retagging ──────────────────────────────────────────────────────────────

// ApplyTagRules re-runs the current tag rules over stored items and makes
// item_tags match them for the tags in scope: rows the rules give are added,
// rows they no longer give are removed (docs/retag-plan.md). A tag with a
// rule whose pattern does not compile is left alone and reported as
// skipped. The whole run is one transaction; with dry_run it is rolled back,
// so the counts are exactly what a real run would do. Changed items are
// pushed to the stream subscribers as item_update after the commit.
func (s *Service) ApplyTagRules(ctx context.Context, req *pb.ApplyTagRulesRequest) (*pb.ApplyTagRulesResponse, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "begin: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if req.TagId != 0 {
		if ok, err := db.TagExists(tx, req.TagId); err != nil {
			return nil, status.Errorf(codes.Internal, "look up tag: %v", err)
		} else if !ok {
			return nil, status.Errorf(codes.NotFound, "tag %d not found", req.TagId)
		}
	}
	if req.SourceId != 0 {
		if ok, err := db.SourceExists(tx, req.SourceId); err != nil {
			return nil, status.Errorf(codes.Internal, "look up source: %v", err)
		} else if !ok {
			return nil, status.Errorf(codes.NotFound, "source %d not found", req.SourceId)
		}
	}

	resp, changed, err := syncTags(tx, req.TagId, req.SourceId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "apply tag rules: %v", err)
	}

	added, removed := 0, 0
	for _, c := range resp.Tags {
		added += int(c.Added)
		removed += int(c.Removed)
	}
	slog.Info("tag rules applied",
		"tag_id", req.TagId,
		"source_id", req.SourceId,
		"dry_run", req.DryRun,
		"items_scanned", resp.ItemsScanned,
		"items_changed", resp.ItemsChanged,
		"added", added,
		"removed", removed,
		"skipped_tags", len(resp.Skipped),
	)

	if req.DryRun {
		return resp, nil
	}
	if err := tx.Commit(); err != nil {
		return nil, status.Errorf(codes.Internal, "commit: %v", err)
	}
	committed = true

	for _, id := range changed {
		s.pushItemUpdate(id)
	}
	return resp, nil
}

// syncTags makes item_tags match the rules for one tag (every tag when
// tagID is 0) over one source's items (every item when sourceID is 0), and
// returns the counts and the IDs of the items that changed, ascending.
// Tags that have no rules are in scope too and lose all their rows.
func syncTags(tx db.QueryExecer, tagID, sourceID int64) (*pb.ApplyTagRulesResponse, []int64, error) {
	names, err := db.ListTagNames(tx)
	if err != nil {
		return nil, nil, fmt.Errorf("list tags: %w", err)
	}
	dbRules, err := db.ListTagRulesForTag(tx, tagID)
	if err != nil {
		return nil, nil, fmt.Errorf("list rules: %w", err)
	}
	rules := make([]tagger.TagRule, len(dbRules))
	for i, r := range dbRules {
		rules[i] = tagger.TagRule{
			ID: r.ID, TagID: r.TagID, TagName: r.TagName,
			Field: r.Field, Pattern: r.Pattern, Priority: int64(r.Priority),
		}
		if r.SourceID != nil {
			rules[i].SourceID = *r.SourceID
		}
	}

	// A tag with any rule that does not compile is skipped as a whole:
	// syncing it on its other rules alone would strip what the broken rule
	// matched.
	compiled, bad := tagger.Compile(rules)
	skip := make(map[int64]bool)
	for _, b := range bad {
		skip[b.Rule.TagID] = true
	}
	usable := compiled[:0:0]
	for _, cr := range compiled {
		if !skip[cr.Rule.TagID] {
			usable = append(usable, cr)
		}
	}

	dbItems, err := db.ListItemsForTagging(tx, sourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("list items: %w", err)
	}
	items := make([]tagger.Item, len(dbItems))
	for i, it := range dbItems {
		items[i] = tagger.Item{ID: it.ID, SourceID: it.SourceID, Title: it.Title, Description: it.Description}
	}

	desired := make(map[db.ItemTag]bool)
	for itemID, tagIDs := range tagger.Desired(usable, items) {
		for _, t := range tagIDs {
			desired[db.ItemTag{ItemID: itemID, TagID: t}] = true
		}
	}
	currentRows, err := db.ListItemTagPairs(tx, tagID, sourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("list item tags: %w", err)
	}
	current := make(map[db.ItemTag]bool, len(currentRows))
	for _, p := range currentRows {
		if !skip[p.TagID] {
			current[p] = true
		}
	}

	var adds, removes []db.ItemTag
	for p := range desired {
		if !current[p] {
			adds = append(adds, p)
		}
	}
	for p := range current {
		if !desired[p] {
			removes = append(removes, p)
		}
	}
	sortPairs(adds)
	sortPairs(removes)

	if err := db.AssignTagsToItems(tx, adds); err != nil {
		return nil, nil, err
	}
	if err := db.RemoveTagsFromItems(tx, removes); err != nil {
		return nil, nil, err
	}

	counts := make(map[int64]*pb.TagSyncCount)
	count := func(tag int64) *pb.TagSyncCount {
		c := counts[tag]
		if c == nil {
			c = &pb.TagSyncCount{TagId: tag, TagName: names[tag]}
			counts[tag] = c
		}
		return c
	}
	changedSet := make(map[int64]bool)
	for _, p := range adds {
		count(p.TagID).Added++
		changedSet[p.ItemID] = true
	}
	for _, p := range removes {
		count(p.TagID).Removed++
		changedSet[p.ItemID] = true
	}

	resp := &pb.ApplyTagRulesResponse{
		ItemsScanned: int32(len(items)),
		ItemsChanged: int32(len(changedSet)),
	}
	for _, c := range counts {
		resp.Tags = append(resp.Tags, c)
	}
	sortCounts(resp.Tags)
	for tag := range skip {
		resp.Skipped = append(resp.Skipped, &pb.TagSyncCount{TagId: tag, TagName: names[tag]})
	}
	sortCounts(resp.Skipped)

	changed := make([]int64, 0, len(changedSet))
	for id := range changedSet {
		changed = append(changed, id)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })
	return resp, changed, nil
}

func sortPairs(p []db.ItemTag) {
	sort.Slice(p, func(i, j int) bool {
		if p[i].ItemID != p[j].ItemID {
			return p[i].ItemID < p[j].ItemID
		}
		return p[i].TagID < p[j].TagID
	})
}

func sortCounts(c []*pb.TagSyncCount) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].TagName != c[j].TagName {
			return c[i].TagName < c[j].TagName
		}
		return c[i].TagId < c[j].TagId
	})
}

// Compile-time check that *sql.Tx can carry the sync.
var _ db.QueryExecer = (*sql.Tx)(nil)
