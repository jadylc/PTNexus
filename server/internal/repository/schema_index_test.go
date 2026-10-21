package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestEnsureSchemaCreatesNameSizeIndex 验证列表页聚合查询依赖的 (name, size) 联合索引。
// 背景：ListTorrentGroupKeysWithFilters / completed_groups 使用 DISTINCT name,size，
// 缺少该索引时 SQLite 走 MULTI-INDEX OR + 临时 B 树去重；COALESCE 过滤条件配合该索引
// 可让预分页查询改用索引顺序扫描，实测约 2 倍提速。
// 参数/返回：无参数；任一断言失败即返回 error。
// 副作用：在内存 SQLite 上执行 EnsureSchema 幂等校验与 EXPLAIN QUERY PLAN。
func TestEnsureSchemaCreatesNameSizeIndex(t *testing.T) {
	store := newTorrentDataRepositoryTestStore(t)

	schema := NewSchemaManager(store)
	if err := schema.EnsureSchema(); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}

	type indexRow struct {
		Name string `gorm:"column:name"`
	}
	indexes := make([]indexRow, 0)
	if err := store.DB.Raw(`PRAGMA index_list(torrents)`).Scan(&indexes).Error; err != nil {
		t.Fatalf("list indexes failed: %v", err)
	}
	found := false
	for _, idx := range indexes {
		if idx.Name == "idx_torrents_name_size" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("idx_torrents_name_size not created, got %v", indexes)
	}

	// EnsureSchema 二次执行需幂等（CREATE INDEX IF NOT EXISTS）
	if err := schema.EnsureSchema(); err != nil {
		t.Fatalf("EnsureSchema second run failed: %v", err)
	}

	// 造 8000 组数据，验证 repository 预分页路径组键数量一致且查询计划走新索引
	tx := store.DB.Begin()
	for group := 0; group < 8000; group++ {
		for site := 0; site < 3; site++ {
			progress := 100.0
			state := "做种中"
			if group%2 == 0 {
				progress = 30.0
				state = "下载中"
			}
			if err := tx.Exec(`INSERT INTO torrents (hash, name, save_path, size, progress, state, sites, downloader_id, last_seen, is_hidden) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
				"hash-"+fmt.Sprint(group)+"-"+fmt.Sprint(site), "Release."+fmt.Sprint(group), "/downloads/movies", int64(group+1)*1000000, progress, state, "site-"+fmt.Sprint(site), "qb", "2026-07-15 00:00:00").Error; err != nil {
				t.Fatalf("insert failed: %v", err)
			}
		}
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	repo := NewTorrentDataRepository(store)
	keys, err := repo.ListTorrentGroupKeysWithFilters(context.Background(), TorrentListFilters{})
	if err != nil {
		t.Fatalf("ListTorrentGroupKeysWithFilters failed: %v", err)
	}
	if len(keys) != 8000 {
		t.Fatalf("expected 8000 group keys, got %d", len(keys))
	}

	type planRow struct {
		Detail string `gorm:"column:detail"`
	}
	plan := make([]planRow, 0)
	if err := store.DB.Raw(`EXPLAIN QUERY PLAN SELECT DISTINCT t.name AS name, t.size AS size FROM torrents t WHERE t.state != '不存在' AND COALESCE(t.is_hidden, 0) = 0`).Scan(&plan).Error; err != nil {
		t.Fatalf("explain failed: %v", err)
	}
	joined := ""
	for _, row := range plan {
		joined += row.Detail + "\n"
	}
	if !strings.Contains(joined, "idx_torrents_name_size") {
		t.Fatalf("repo SQL did not use idx_torrents_name_size:\n%s", joined)
	}
}
