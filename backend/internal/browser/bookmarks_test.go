package browser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"facade/backend/internal/config"
)

func TestNormalizeBookmarkURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "trailing slash", in: "https://github.com", want: "https://github.com/"},
		{name: "already slashed", in: "https://github.com/", want: "https://github.com/"},
		{name: "upper host", in: "HTTPS://GitHub.COM/Path", want: "https://github.com/Path"},
		{name: "missing scheme", in: "github.com", want: "https://github.com/"},
		{name: "trim space", in: "  https://example.com  ", want: "https://example.com/"},
		{name: "empty", in: "   ", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeBookmarkURL(tc.in); got != tc.want {
				t.Fatalf("NormalizeBookmarkURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnsureDefaultBookmarksDedupesNormalizedURL(t *testing.T) {
	userDataDir := t.TempDir()
	profileDir := filepath.Join(userDataDir, "Default")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	existing := newEmptyBookmarkRoot(toChromiumTime(chromiumEpoch))
	bar := existing["roots"].(map[string]interface{})["bookmark_bar"].(map[string]interface{})
	bar["children"] = []interface{}{
		map[string]interface{}{
			"date_added": "1",
			"guid":       "existing",
			"id":         "10",
			"name":       "GitHub",
			"type":       "url",
			"url":        "https://github.com/",
		},
	}
	raw, err := json.MarshalIndent(existing, "", "   ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "Bookmarks"), raw, 0o644); err != nil {
		t.Fatalf("write bookmarks: %v", err)
	}

	bookmarks := []config.BrowserBookmark{
		{Name: "GitHub", URL: "https://github.com"},
		{Name: "GitHub Upper", URL: "HTTPS://GITHUB.COM"},
		{Name: "GitHub No Scheme", URL: "github.com"},
	}
	if err := EnsureDefaultBookmarks(userDataDir, bookmarks); err != nil {
		t.Fatalf("EnsureDefaultBookmarks: %v", err)
	}

	count := bookmarkBarURLCount(t, userDataDir)
	if count != 1 {
		t.Fatalf("expected 1 bookmark after normalized dedupe, got %d", count)
	}

	if err := EnsureDefaultBookmarks(userDataDir, bookmarks); err != nil {
		t.Fatalf("second EnsureDefaultBookmarks: %v", err)
	}
	if count := bookmarkBarURLCount(t, userDataDir); count != 1 {
		t.Fatalf("expected still 1 bookmark after second ensure, got %d", count)
	}
}

func TestEnsureDefaultBookmarksIsIdempotent(t *testing.T) {
	userDataDir := t.TempDir()
	bookmarks := []config.BrowserBookmark{
		{Name: "Example", URL: "https://example.com"},
	}
	if err := EnsureDefaultBookmarks(userDataDir, bookmarks); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if err := EnsureDefaultBookmarks(userDataDir, bookmarks); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if count := bookmarkBarURLCount(t, userDataDir); count != 1 {
		t.Fatalf("expected 1 bookmark, got %d", count)
	}
	data, err := os.ReadFile(filepath.Join(userDataDir, "Default", "Bookmarks"))
	if err != nil {
		t.Fatalf("read bookmarks: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	children := extractBarChildren(root)
	node, ok := children[0].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected child type: %#v", children[0])
	}
	if got, _ := node["url"].(string); got != "https://example.com/" {
		t.Fatalf("stored url = %q, want https://example.com/", got)
	}
}

func bookmarkBarURLCount(t *testing.T, userDataDir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(userDataDir, "Default", "Bookmarks"))
	if err != nil {
		t.Fatalf("read bookmarks: %v", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	count := 0
	for _, child := range extractBarChildren(root) {
		node, ok := child.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := node["url"].(string); ok {
			count++
		}
	}
	return count
}
