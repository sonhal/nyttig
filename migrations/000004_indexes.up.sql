-- ListItems orders by published (newest first, NULLs last) and fetched_at,
-- on every stream connection and every /api/items page. Without an index
-- that sorts the whole table each time.
CREATE INDEX idx_items_published ON items (published DESC, fetched_at DESC);

-- Tag filters look up item_tags by tag; the primary key (item_id, tag_id)
-- only serves lookups by item. UNIQUE(source_id, guid) already indexes
-- source_id.
CREATE INDEX idx_item_tags_tag_id ON item_tags (tag_id);
