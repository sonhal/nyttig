-- A saved view can limit the feed to a rolling window ("24h", "7d", "2w",
-- "1mo", "1y"). The duration string is stored as typed; empty = no window.
ALTER TABLE saved_views ADD COLUMN since TEXT NOT NULL DEFAULT '';
