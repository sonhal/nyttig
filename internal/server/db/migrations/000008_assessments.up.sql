-- Assessments: a score (0 to 1) and/or a note that an external assessor
-- attaches to an item, optionally scoped to one tag. Each assessor's work is
-- kept apart; nothing ever merges scores from different assessors.
CREATE TABLE assessors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,          -- what the score means; shown in the UI
    color       TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE assessments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id     INTEGER NOT NULL REFERENCES items(id)     ON DELETE CASCADE,
    assessor_id INTEGER NOT NULL REFERENCES assessors(id) ON DELETE CASCADE,
    tag_id      INTEGER          REFERENCES tags(id)      ON DELETE CASCADE, -- NULL = whole item
    score       REAL CHECK (score IS NULL OR (score >= 0 AND score <= 1)),
    note        TEXT,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (score IS NOT NULL OR note IS NOT NULL)
);

-- One assessment per item, assessor and tag; NULL tags compare equal here.
CREATE UNIQUE INDEX idx_assessments_key ON assessments (item_id, assessor_id, IFNULL(tag_id, 0));
-- min_score and sort:score look assessments up by assessor and score.
CREATE INDEX idx_assessments_assessor_score ON assessments (assessor_id, score);
-- ON DELETE CASCADE from tags looks rows up by tag_id.
CREATE INDEX idx_assessments_tag_id ON assessments (tag_id);

-- Saved views gain the assessor filter (and keep migration 7's since column).
-- SQLite cannot change a CHECK in place, so the table is rebuilt to let sort
-- be 'score'. Nothing references saved_views, so the rebuild is safe with
-- foreign keys on.
CREATE TABLE saved_views_new (
    id            INTEGER PRIMARY KEY,
    name          TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    search        TEXT    NOT NULL DEFAULT '',
    source_id     INTEGER REFERENCES sources(id)   ON DELETE SET NULL,
    tag_id        INTEGER REFERENCES tags(id)      ON DELETE SET NULL,
    sort          TEXT    NOT NULL DEFAULT 'newest' CHECK (sort IN ('newest','oldest','score')),
    unviewed_only INTEGER NOT NULL DEFAULT 0,
    favorite      INTEGER NOT NULL DEFAULT 0,
    position      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    since         TEXT    NOT NULL DEFAULT '',
    assessor_id   INTEGER REFERENCES assessors(id) ON DELETE SET NULL,
    min_score     REAL,
    unassessed_by INTEGER REFERENCES assessors(id) ON DELETE SET NULL
);

INSERT INTO saved_views_new
    (id, name, search, source_id, tag_id, sort, unviewed_only, favorite, position, created_at, since)
SELECT id, name, search, source_id, tag_id, sort, unviewed_only, favorite, position, created_at, since
FROM saved_views;

DROP TABLE saved_views;
ALTER TABLE saved_views_new RENAME TO saved_views;

CREATE INDEX idx_saved_views_source_id ON saved_views (source_id);
CREATE INDEX idx_saved_views_tag_id ON saved_views (tag_id);
CREATE INDEX idx_saved_views_assessor_id ON saved_views (assessor_id);
CREATE INDEX idx_saved_views_unassessed_by ON saved_views (unassessed_by);

-- Deleting an assessor keeps the views that use it and drops the fields.
-- ON DELETE SET NULL clears assessor_id and unassessed_by; this trigger also
-- clears min_score and turns sort 'score' back into 'newest', which would
-- otherwise be left without an assessor.
CREATE TRIGGER assessors_clear_views BEFORE DELETE ON assessors
BEGIN
    UPDATE saved_views
    SET min_score = NULL,
        sort = CASE WHEN sort = 'score' THEN 'newest' ELSE sort END
    WHERE assessor_id = OLD.id;
END;
