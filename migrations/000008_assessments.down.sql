DROP TRIGGER assessors_clear_views;

CREATE TABLE saved_views_old (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    search        TEXT    NOT NULL DEFAULT '',
    source_id     INTEGER REFERENCES sources(id) ON DELETE SET NULL,
    tag_id        INTEGER REFERENCES tags(id)    ON DELETE SET NULL,
    sort          TEXT    NOT NULL DEFAULT 'newest' CHECK (sort IN ('newest','oldest')),
    unviewed_only INTEGER NOT NULL DEFAULT 0,
    favorite      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    since         TEXT    NOT NULL DEFAULT ''
);

INSERT INTO saved_views_old
    (id, name, search, source_id, tag_id, sort, unviewed_only, favorite, position, created_at, since)
SELECT id, name, search, source_id, tag_id,
       CASE WHEN sort = 'score' THEN 'newest' ELSE sort END,
       unviewed_only, favorite, position, created_at, since
FROM saved_views;

DROP TABLE saved_views;
ALTER TABLE saved_views_old RENAME TO saved_views;
CREATE INDEX idx_saved_views_source_id ON saved_views (source_id);
CREATE INDEX idx_saved_views_tag_id ON saved_views (tag_id);

DROP TABLE assessments;
DROP TABLE assessors;
