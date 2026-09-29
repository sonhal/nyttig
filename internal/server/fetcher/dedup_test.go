package fetcher

import (
	"testing"
)

func TestDedupKey_GuidPresent(t *testing.T) {
	// When GUID is present and non-empty, it should be used directly.
	key1 := DedupKey("https://example.com/guid/123", "https://example.com/article/456")
	key2 := DedupKey("https://example.com/guid/123", "https://different-link.com/article")

	if key1 != "https://example.com/guid/123" {
		t.Errorf("expected guid value, got %q", key1)
	}
	if key1 != key2 {
		t.Errorf("same GUID with different link should produce same key: %q vs %q", key1, key2)
	}
}

func TestDedupKey_GuidEmpty(t *testing.T) {
	// When GUID is empty string but link is present, fall back to SHA-256 of link.
	key := DedupKey("", "https://example.com/article")
	// SHA-256 of "https://example.com/article"
	expected := "632538290468e7a39c06323c9e3ae98f31072d641cbb37ea37917f56bbeb5539"
	if key != expected {
		t.Errorf("expected SHA-256 of link, got %q", key)
	}
}

func TestDedupKey_GuidMissing(t *testing.T) {
	// When GUID is missing (empty), different links produce different keys.
	key1 := DedupKey("", "https://blog.example.com/post-1")
	key2 := DedupKey("", "https://blog.example.com/post-2")
	key3 := DedupKey("", "https://blog.example.com/post-1") // same as key1

	if key1 == "" {
		t.Error("dedup key should not be empty when link is provided")
	}
	if key1 == key2 {
		t.Errorf("different links should produce different keys: %q vs %q", key1, key2)
	}
	if key1 != key3 {
		t.Errorf("same link should produce same key: %q vs %q", key1, key3)
	}
}

func TestDedupKey_DuplicateScenarios(t *testing.T) {
	// Items with same non-empty GUID should be duplicates (same key).
	a1 := DedupKey("guid-abc", "https://x.com/1")
	a2 := DedupKey("guid-abc", "https://y.com/2")
	if a1 != a2 {
		t.Errorf("same GUID should deduplicate: %q vs %q", a1, a2)
	}

	// Items with empty GUID but same link should be duplicates.
	b1 := DedupKey("", "https://same.link/article")
	b2 := DedupKey("", "https://same.link/article")
	if b1 != b2 {
		t.Errorf("same link (no GUID) should deduplicate: %q vs %q", b1, b2)
	}

	// Items with different non-empty GUIDs should NOT be duplicates.
	c1 := DedupKey("guid-1", "https://same.link/x")
	c2 := DedupKey("guid-2", "https://same.link/x")
	if c1 == c2 {
		t.Errorf("different GUIDs should not be duplicates: %q vs %q", c1, c2)
	}

	// Item with GUID present should not collide with same-link no-GUID item.
	d1 := DedupKey("custom-guid", "https://some.link/a")
	d2 := DedupKey("", "https://some.link/a")
	if d1 == d2 {
		t.Errorf("GUID-based key should not equal link-hash key: %q vs %q", d1, d2)
	}
}

func TestDedupKey_EmptyGUIDAndLink(t *testing.T) {
	// Edge case: both empty. SHA-256 of empty string.
	key := DedupKey("", "")
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if key != expected {
		t.Errorf("expected SHA-256 of empty string, got %q", key)
	}
}
