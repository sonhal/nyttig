-- Saved views are persistent, named feed filters. A view that names a source
-- or tag keeps existing when that source or tag is deleted: the field is
-- cleared and the view stops filtering on it.
CREATE TABLE saved_views (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    search        TEXT    NOT NULL DEFAULT '',
    source_id     INTEGER REFERENCES sources(id) ON DELETE SET NULL,
    tag_id        INTEGER REFERENCES tags(id)    ON DELETE SET NULL,
    sort          TEXT    NOT NULL DEFAULT 'newest' CHECK (sort IN ('newest','oldest')),
    unviewed_only INTEGER NOT NULL DEFAULT 0,
    favorite      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- ON DELETE SET NULL looks the referencing rows up by these columns.
CREATE INDEX idx_saved_views_source_id ON saved_views (source_id);
CREATE INDEX idx_saved_views_tag_id ON saved_views (tag_id);
