package browser

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"facade/backend/internal/config"
)

// NormalizeBookmarkURL normalizes a bookmark URL for stable dedupe against Chrome's Bookmarks file.
// Missing scheme defaults to https://; scheme/host are lowercased; empty path becomes "/".
func NormalizeBookmarkURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	candidate := trimmed
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

// chromiumEpoch 是 Chrome FILETIME 的起始时间（1601-01-01 UTC）
var chromiumEpoch = time.Date(1601, 1, 1, 0, 0, 0, 0, time.UTC)

func toChromiumTime(t time.Time) string {
	return fmt.Sprintf("%d", t.Sub(chromiumEpoch).Microseconds())
}

// EnsureDefaultBookmarks 将默认书签合并到书签栏（已存在的 URL 不重复添加）
func EnsureDefaultBookmarks(userDataDir string, bookmarks []config.BrowserBookmark) error {
	if len(bookmarks) == 0 {
		return nil
	}

	profileDir := filepath.Join(userDataDir, "Default")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		return fmt.Errorf("创建 profile 目录失败: %w", err)
	}

	bookmarksPath := filepath.Join(profileDir, "Bookmarks")

	// 尝试读取已有书签文件
	var root map[string]interface{}
	if data, err := os.ReadFile(bookmarksPath); err == nil {
		_ = json.Unmarshal(data, &root)
	}

	now := toChromiumTime(time.Now())

	// 初始化空结构
	if root == nil {
		root = newEmptyBookmarkRoot(now)
	}

	// 取出 bookmark_bar children，按整个书签树收集已有 URL 集合
	barChildren := extractBarChildren(root)
	existingURLs := collectRootURLs(root)

	// 计算当前最大 id，用于分配新 id
	maxID := findMaxID(root)

	// 把不存在的默认书签追加进去（URL 规范化后再去重）
	added := false
	for _, b := range bookmarks {
		name := strings.TrimSpace(b.Name)
		normalizedURL := NormalizeBookmarkURL(b.URL)
		if name == "" || normalizedURL == "" {
			continue
		}
		if existingURLs[normalizedURL] {
			continue
		}
		maxID++
		barChildren = append(barChildren, map[string]interface{}{
			"date_added":     now,
			"date_last_used": "0",
			"guid":           bookmarkGUID(normalizedURL),
			"id":             fmt.Sprintf("%d", maxID),
			"meta_info":      map[string]string{"power_bookmark_meta": ""},
			"name":           name,
			"type":           "url",
			"url":            normalizedURL,
		})
		existingURLs[normalizedURL] = true
		added = true
	}

	if !added {
		return nil
	}

	// 写回
	roots := root["roots"].(map[string]interface{})
	bar := roots["bookmark_bar"].(map[string]interface{})
	bar["children"] = barChildren
	bar["date_modified"] = now
	roots["bookmark_bar"] = bar
	root["roots"] = roots

	out, err := json.MarshalIndent(root, "", "   ")
	if err != nil {
		return fmt.Errorf("序列化书签失败: %w", err)
	}
	return os.WriteFile(bookmarksPath, out, 0644)
}

// newEmptyBookmarkRoot 构建一个空的书签根结构
func newEmptyBookmarkRoot(now string) map[string]interface{} {
	return map[string]interface{}{
		"checksum": "",
		"version":  1,
		"roots": map[string]interface{}{
			"bookmark_bar": map[string]interface{}{
				"children":       []interface{}{},
				"date_added":     now,
				"date_last_used": "0",
				"date_modified":  now,
				"guid":           "0bc5d13f-2cba-5d74-951f-3f233fe6c908",
				"id":             "1",
				"name":           "书签栏",
				"type":           "folder",
			},
			"other": map[string]interface{}{
				"children":       []interface{}{},
				"date_added":     now,
				"date_last_used": "0",
				"date_modified":  "0",
				"guid":           "82b081ec-3dd3-529c-8475-ab6c344590dd",
				"id":             "2",
				"name":           "其他书签",
				"type":           "folder",
			},
			"synced": map[string]interface{}{
				"children":       []interface{}{},
				"date_added":     now,
				"date_last_used": "0",
				"date_modified":  "0",
				"guid":           "4cf2e351-0e85-532b-bb37-df045d8f8d0f",
				"id":             "3",
				"name":           "移动设备书签",
				"type":           "folder",
			},
		},
	}
}

// extractBarChildren 从根结构中提取书签栏 children
func extractBarChildren(root map[string]interface{}) []interface{} {
	var children []interface{}

	roots, ok := root["roots"].(map[string]interface{})
	if !ok {
		root["roots"] = map[string]interface{}{
			"bookmark_bar": map[string]interface{}{
				"children": []interface{}{},
				"type":     "folder",
				"name":     "书签栏",
			},
		}
		return children
	}

	bar, ok := roots["bookmark_bar"].(map[string]interface{})
	if !ok {
		roots["bookmark_bar"] = map[string]interface{}{
			"children": []interface{}{},
			"type":     "folder",
			"name":     "书签栏",
		}
		root["roots"] = roots
		return children
	}

	if c, ok := bar["children"].([]interface{}); ok {
		children = c
	}
	return children
}

func collectRootURLs(root map[string]interface{}) map[string]bool {
	existing := map[string]bool{}
	roots, ok := root["roots"].(map[string]interface{})
	if !ok {
		return existing
	}
	for _, item := range roots {
		folder, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if children, ok := folder["children"].([]interface{}); ok {
			collectURLs(children, existing)
		}
	}
	return existing
}

// collectURLs 递归收集所有书签 URL（有 url 字段即收集，规范化后去重）
func collectURLs(nodes []interface{}, out map[string]bool) {
	for _, n := range nodes {
		node, ok := n.(map[string]interface{})
		if !ok {
			continue
		}
		if u, ok := node["url"].(string); ok {
			if normalized := NormalizeBookmarkURL(u); normalized != "" {
				out[normalized] = true
			}
		}
		if sub, ok := node["children"].([]interface{}); ok {
			collectURLs(sub, out)
		}
	}
}

// findMaxID 遍历整个书签树找到最大数字 id
func findMaxID(root map[string]interface{}) int {
	max := 0
	roots, ok := root["roots"].(map[string]interface{})
	if !ok {
		return max
	}
	for _, v := range roots {
		if folder, ok := v.(map[string]interface{}); ok {
			scanMaxID(folder, &max)
		}
	}
	return max
}

func scanMaxID(node map[string]interface{}, max *int) {
	if idStr, ok := node["id"].(string); ok {
		var n int
		fmt.Sscanf(idStr, "%d", &n)
		if n > *max {
			*max = n
		}
	}
	if children, ok := node["children"].([]interface{}); ok {
		for _, c := range children {
			if child, ok := c.(map[string]interface{}); ok {
				scanMaxID(child, max)
			}
		}
	}
}

// bookmarkGUID 根据 URL 生成稳定伪 GUID
func bookmarkGUID(url string) string {
	h := uint64(14695981039346656037)
	for _, c := range url {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h&0xffffffff, (h>>32)&0xffff,
		(h>>48)&0x0fff|0x4000,
		(h>>16)&0x3fff|0x8000,
		h&0xffffffffffff,
	)
}
