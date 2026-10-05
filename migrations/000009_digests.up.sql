-- Digests: documents an assessor writes about one to many items, kept as a
-- history in named series. See docs/digests-plan.md.
--
-- The dates of digests are written by the db layer in Go (UTC, whole
-- seconds), the same text shape as items.published, so that ordering by text
-- is ordering by time.
CREATE TABLE digest_series (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    assessor_id INTEGER NOT NULL REFERENCES assessors(id) ON DELETE CASCADE,
    name        TEXT    NOT NULL COLLATE NOCASE,
    description TEXT,
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (assessor_id, name)
);

CREATE TABLE digests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id    INTEGER NOT NULL REFERENCES digest_series(id) ON DELETE CASCADE,
    title        TEXT    NOT NULL,
    body         TEXT    NOT NULL,
    period_start DATETIME NOT NULL,
    period_end   DATETIME NOT NULL,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    CHECK (period_end >= period_start)
);
-- A series' history, newest period first.
CREATE INDEX idx_digests_series_period ON digests (series_id, period_end DESC, id DESC);

CREATE TABLE digest_items (
    digest_id INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    item_id   INTEGER NOT NULL REFERENCES items(id)   ON DELETE CASCADE,
    PRIMARY KEY (digest_id, item_id)
);
-- ON DELETE CASCADE from items looks rows up by item_id.
CREATE INDEX idx_digest_items_item_id ON digest_items (item_id);

CREATE TABLE digest_inputs (
    digest_id INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    input_id  INTEGER NOT NULL REFERENCES digests(id) ON DELETE CASCADE,
    PRIMARY KEY (digest_id, input_id),
    CHECK (digest_id <> input_id)
);
CREATE INDEX idx_digest_inputs_input_id ON digest_inputs (input_id);
