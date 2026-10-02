-- A tag can have several parents (a DAG). Filtering by a tag also matches
-- items tagged with any tag below it; item_tags keeps recording only what a
-- rule matched. Deleting a tag removes its edges in both directions, so a
-- child left without a parent row is top-level.
CREATE TABLE tag_parents (
    child_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    parent_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (child_id, parent_id),
    CHECK (child_id <> parent_id)
);

-- The subtree CTE walks parent -> children; the primary key only serves
-- child -> parents.
CREATE INDEX idx_tag_parents_parent_id ON tag_parents (parent_id);
