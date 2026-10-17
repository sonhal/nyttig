-- A saved view can show only some of the feed's columns: canonical names
-- joined by commas, in display order (internal/columns); empty = all.
ALTER TABLE saved_views ADD COLUMN columns TEXT NOT NULL DEFAULT '';
