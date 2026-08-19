package backend

import (
	"facade/backend/internal/logger"
	"fmt"
	"strings"
	"time"
)

func normalizeBrowserTagList(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		key := strings.ToLower(tag)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

// BrowserCreateTag 保存一个未分配给实例的标签。
func (a *App) BrowserCreateTag(tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return fmt.Errorf("标签名称不能为空")
	}
	if a.browserMgr == nil || a.browserMgr.TagDAO == nil {
		return fmt.Errorf("标签存储未初始化")
	}
	return a.browserMgr.TagDAO.Upsert(tag)
}

// BrowserProfileBatchSetTags 批量为实例设置标签（追加模式：将 tags 加入已有标签；replace 模式：直接替换）
func (a *App) BrowserProfileBatchSetTags(profileIds []string, tags []string, replace bool) error {
	log := logger.New("Browser")
	tags = normalizeBrowserTagList(tags)
	if len(tags) == 0 && !replace {
		return nil
	}
	if len(tags) > 0 && a.browserMgr.TagDAO != nil {
		for _, tag := range tags {
			if err := a.browserMgr.TagDAO.Upsert(tag); err != nil {
				return err
			}
		}
	}

	a.browserMgr.InitData()
	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()

	for _, profileID := range profileIds {
		profile, exists := a.browserMgr.Profiles[profileID]
		if !exists {
			continue
		}
		if replace {
			profile.Tags = append([]string{}, tags...)
		} else {
			existing := make(map[string]struct{})
			for _, tag := range profile.Tags {
				existing[strings.ToLower(strings.TrimSpace(tag))] = struct{}{}
			}
			for _, tag := range tags {
				key := strings.ToLower(tag)
				if _, ok := existing[key]; !ok {
					profile.Tags = append(profile.Tags, tag)
					existing[key] = struct{}{}
				}
			}
		}
		profile.UpdatedAt = time.Now().Format(time.RFC3339)
		if a.browserMgr.ProfileDAO != nil {
			if err := a.browserMgr.ProfileDAO.Upsert(profile); err != nil {
				log.Error("批量设置标签失败", logger.F("profile_id", profileID), logger.F("error", err))
				return err
			}
		}
	}
	return nil
}

// BrowserProfileBatchRemoveTags 批量从实例移除指定标签
func (a *App) BrowserProfileBatchRemoveTags(profileIds []string, tags []string) error {
	log := logger.New("Browser")
	a.browserMgr.InitData()
	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()

	removeSet := make(map[string]struct{})
	for _, tag := range normalizeBrowserTagList(tags) {
		removeSet[strings.ToLower(tag)] = struct{}{}
	}

	for _, profileID := range profileIds {
		profile, exists := a.browserMgr.Profiles[profileID]
		if !exists {
			continue
		}
		filtered := profile.Tags[:0]
		for _, tag := range profile.Tags {
			if _, ok := removeSet[strings.ToLower(strings.TrimSpace(tag))]; !ok {
				filtered = append(filtered, tag)
			}
		}
		profile.Tags = filtered
		profile.UpdatedAt = time.Now().Format(time.RFC3339)
		if a.browserMgr.ProfileDAO != nil {
			if err := a.browserMgr.ProfileDAO.Upsert(profile); err != nil {
				log.Error("批量移除标签失败", logger.F("profile_id", profileID), logger.F("error", err))
				return err
			}
		}
	}
	return nil
}

// BrowserRenameTag 重命名所有实例中的指定标签
func (a *App) BrowserRenameTag(oldName string, newName string) error {
	log := logger.New("Browser")
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return fmt.Errorf("标签名称不能为空")
	}
	if a.browserMgr != nil && a.browserMgr.TagDAO != nil {
		if err := a.browserMgr.TagDAO.Rename(oldName, newName); err != nil {
			return err
		}
	}

	a.browserMgr.InitData()
	a.browserMgr.Mutex.Lock()
	defer a.browserMgr.Mutex.Unlock()

	changedCount := 0
	for profileID, profile := range a.browserMgr.Profiles {
		tagChanged := false
		var newTags []string
		for _, tag := range profile.Tags {
			if strings.EqualFold(tag, oldName) {
				newTags = append(newTags, newName)
				tagChanged = true
			} else {
				newTags = append(newTags, tag)
			}
		}

		if tagChanged {
			uniqueTags := make([]string, 0)
			seen := make(map[string]struct{})
			for _, tag := range newTags {
				if _, ok := seen[tag]; !ok {
					uniqueTags = append(uniqueTags, tag)
					seen[tag] = struct{}{}
				}
			}

			profile.Tags = uniqueTags
			profile.UpdatedAt = time.Now().Format(time.RFC3339)
			if a.browserMgr.ProfileDAO != nil {
				if err := a.browserMgr.ProfileDAO.Upsert(profile); err != nil {
					log.Error("重命名标签保存失败", logger.F("profile_id", profileID), logger.F("error", err))
					return err
				}
			}
			changedCount++
		}
	}

	if changedCount > 0 && a.browserMgr.ProfileDAO == nil {
		if err := a.browserMgr.SaveProfiles(); err != nil {
			return err
		}
	}

	if changedCount > 0 {
		log.Info("重命名标签成功", logger.F("old", oldName), logger.F("new", newName), logger.F("changed_profiles", changedCount))
	}
	return nil
}
