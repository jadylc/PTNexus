package fetch

import (
	"fmt"
	"strings"
	"testing"
)

// TestRunBatchFetchFallsBackToNextSource 高优站点抓取失败时，自动尝试后续候选站点。
func TestRunBatchFetchFallsBackToNextSource(t *testing.T) {
	rows := []map[string]any{
		{
			"name":      "Some.Torrent.2024",
			"sites":     "高优站,低优站",
			"hash":      "hash1",
			"details":   "",
			"save_path": "/downloads/qb",
			"site_torrent_ids": map[string]string{
				"高优站": "111",
				"低优站": "222",
			},
		},
	}

	attemptedSites := make([]string, 0, 2)
	RunBatchFetch(
		[]string{"Some.Torrent.2024"},
		[]string{"高优站", "低优站"},
		nil,
		rows,
		BatchFetchRunnerDeps{
			FetchAndStore: func(payload map[string]any) (map[string]any, int) {
				attemptedSites = append(attemptedSites, toStringAny(payload["sourceSite"], ""))
				if payload["sourceSite"] == "高优站" {
					// 高优站点失败（如 CF 挑战、详情页 404）
					return map[string]any{"success": false, "message": "详情页抓取失败"}, 500
				}
				return map[string]any{"success": true, "task_id": "t1"}, 200
			},
			OnResult: func(bool, map[string]any) {},
		},
	)

	if len(attemptedSites) != 2 {
		t.Fatalf("expected 2 attempts (fallback to next source), got %v", attemptedSites)
	}
	if attemptedSites[0] != "高优站" || attemptedSites[1] != "低优站" {
		t.Fatalf("expected fallback order 高优站->低优站, got %v", attemptedSites)
	}
}

// TestRunBatchFetchAllSourcesFail 全部候选失败时报错并附各站点原因。
func TestRunBatchFetchAllSourcesFail(t *testing.T) {
	rows := []map[string]any{
		{
			"name":      "Some.Torrent.2024",
			"sites":     "站A,站B",
			"hash":      "hash1",
			"details":   "",
			"save_path": "/downloads/qb",
			"site_torrent_ids": map[string]string{
				"站A": "111",
				"站B": "222",
			},
		},
	}

	var lastResult map[string]any
	RunBatchFetch(
		[]string{"Some.Torrent.2024"},
		[]string{"站A", "站B"},
		nil,
		rows,
		BatchFetchRunnerDeps{
			FetchAndStore: func(payload map[string]any) (map[string]any, int) {
				return map[string]any{
					"success": false,
					"message": fmt.Sprintf("站点 %s 失败", payload["sourceSite"]),
				}, 500
			},
			OnResult: func(success bool, result map[string]any) {
				lastResult = result
			},
		},
	)

	if lastResult["status"] != "failed" {
		t.Fatalf("expected failed status, got %v", lastResult["status"])
	}
	reason := toStringAny(lastResult["reason"], "")
	if !strings.Contains(reason, "站A") || !strings.Contains(reason, "站B") {
		t.Fatalf("expected failure reason to include both sites, got %q", reason)
	}
}
