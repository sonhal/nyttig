-- items.published is sorted as text, so every value must have one shape. The
-- driver stores a time.Time as "2006-01-02 15:04:05.999999999-07:00" in the
-- value's own UTC offset, which does not compare chronologically across
-- offsets. The fetcher now writes UTC with whole seconds, which the driver
-- stores as "YYYY-MM-DD HH:MM:SS+00:00". Rewrite the existing rows to the
-- same text. Values SQLite cannot parse are left as they are.
UPDATE items
SET published = strftime('%Y-%m-%d %H:%M:%S+00:00', published)
WHERE published IS NOT NULL
  AND strftime('%Y-%m-%d %H:%M:%S+00:00', published) IS NOT NULL
  AND strftime('%Y-%m-%d %H:%M:%S+00:00', published) <> published;
