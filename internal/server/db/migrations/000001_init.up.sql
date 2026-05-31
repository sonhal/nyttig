CREATE TABLE sources (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL UNIQUE,
    type        TEXT NOT NULL DEFAULT 'rss',
    refresh_sec INTEGER NOT NULL DEFAULT 3600,
    enabled     BOOLEAN NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_fetch  DATETIME,
    fetch_error TEXT
);

CREATE TABLE items (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id   INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    guid        TEXT NOT NULL,
    link        TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT,
    author      TEXT,
    published   DATETIME,
    fetched_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(source_id, guid)
);

CREATE TABLE tags (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    name    TEXT NOT NULL UNIQUE,
    color   TEXT
);

CREATE TABLE tag_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    source_id  INTEGER REFERENCES sources(id) ON DELETE CASCADE,
    tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    field      TEXT NOT NULL DEFAULT 'both',
    pattern    TEXT NOT NULL,
    priority   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE item_tags (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (item_id, tag_id)
);

CREATE TABLE view_state (
    item_id   INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    viewed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE VIRTUAL TABLE items_fts USING fts5(title, description, content='items', content_rowid='id');

CREATE TRIGGER items_ai AFTER INSERT ON items BEGIN
    INSERT INTO items_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;

CREATE TRIGGER items_ad AFTER DELETE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, description) VALUES ('delete', old.id, old.title, old.description);
END;

CREATE TRIGGER items_au AFTER UPDATE ON items BEGIN
    INSERT INTO items_fts(items_fts, rowid, title, description) VALUES ('delete', old.id, old.title, old.description);
    INSERT INTO items_fts(rowid, title, description) VALUES (new.id, new.title, new.description);
END;
