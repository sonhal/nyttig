-- Archives: self-contained copies of the articles behind items. The files
-- live on disk (archive_dir); this table is the index and the work queue.
--
-- An archive outlives its item: link, title and source_name are copies, and
-- item_id is cleared when the item (or its source) is deleted. UNIQUE allows
-- any number of NULLs, so orphaned archives do not collide.
--
-- Dates are written by the db layer in Go (UTC, whole seconds), the text
-- shape of items.published.
CREATE TABLE archives (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id        INTEGER UNIQUE REFERENCES items(id) ON DELETE SET NULL,
    link           TEXT    NOT NULL,
    title          TEXT    NOT NULL,
    source_name    TEXT    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'running', 'done', 'partial', 'failed')),
    error          TEXT    NOT NULL DEFAULT '',
    note           TEXT    NOT NULL DEFAULT '',
    final_url      TEXT    NOT NULL DEFAULT '',
    content_type   TEXT    NOT NULL DEFAULT '',
    http_status    INTEGER NOT NULL DEFAULT 0,
    size           INTEGER NOT NULL DEFAULT 0,
    original_size  INTEGER NOT NULL DEFAULT 0,
    missing_assets INTEGER NOT NULL DEFAULT 0,
    requested_at   DATETIME NOT NULL,
    started_at     DATETIME,
    finished_at    DATETIME
);
CREATE INDEX idx_archives_status_requested ON archives (status, requested_at);
