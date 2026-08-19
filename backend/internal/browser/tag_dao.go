package browser

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// TagDAO persists tags that are not currently assigned to any profile.
type TagDAO interface {
	List() ([]string, error)
	Upsert(name string) error
	Rename(oldName, newName string) error
}

type SQLiteTagDAO struct {
	db *sql.DB
}

func NewSQLiteTagDAO(db *sql.DB) *SQLiteTagDAO {
	return &SQLiteTagDAO{db: db}
}

func (d *SQLiteTagDAO) List() ([]string, error) {
	rows, err := d.db.Query(`SELECT tag_name FROM browser_tags ORDER BY lower(tag_name) ASC`)
	if err != nil {
		return nil, fmt.Errorf("查询标签列表失败: %w", err)
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("读取标签行失败: %w", err)
		}
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(tags, func(i, j int) bool {
		return strings.ToLower(tags[i]) < strings.ToLower(tags[j])
	})
	return tags, nil
}

func (d *SQLiteTagDAO) Upsert(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	_, err := d.db.Exec(`
		INSERT INTO browser_tags (tag_name, created_at, updated_at)
		VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(tag_name) DO UPDATE SET updated_at = excluded.updated_at`,
		name,
	)
	if err != nil {
		return fmt.Errorf("保存标签失败: %w", err)
	}
	return nil
}

func (d *SQLiteTagDAO) Rename(oldName, newName string) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" || oldName == newName {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	if strings.EqualFold(oldName, newName) {
		if _, err := tx.Exec(`DELETE FROM browser_tags WHERE tag_name = ?`, oldName); err != nil {
			return fmt.Errorf("删除旧标签失败: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO browser_tags (tag_name, created_at, updated_at)
			VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT(tag_name) DO UPDATE SET updated_at = excluded.updated_at`,
			newName,
		); err != nil {
			return fmt.Errorf("保存新标签失败: %w", err)
		}
		return tx.Commit()
	}

	if _, err := tx.Exec(`
		INSERT INTO browser_tags (tag_name, created_at, updated_at)
		VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(tag_name) DO UPDATE SET updated_at = excluded.updated_at`,
		newName,
	); err != nil {
		return fmt.Errorf("保存新标签失败: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM browser_tags WHERE lower(tag_name) = lower(?)`, oldName); err != nil {
		return fmt.Errorf("删除旧标签失败: %w", err)
	}
	return tx.Commit()
}
