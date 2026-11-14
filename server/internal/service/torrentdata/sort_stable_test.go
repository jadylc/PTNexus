package torrentdata

import (
	"testing"
)

// TestSortDataStableForEqualValues 回归 2026.11：按做种数（site_count）排序时
// 同值行顺序不稳定。原因是聚合 map 遍历顺序随机，而数值排序在同值时直接返回
// false，导致相对顺序随输入变化——翻页会重复/漏项。
// 修复后：同值行按名称（再按体积）确定性排序。
func TestSortDataStableForEqualValues(t *testing.T) {
	service := &TorrentDataService{}

	// 构造两组同 site_count 但顺序相反的数据
	build := func(reversed bool) []map[string]any {
		items := []map[string]any{
			{"name": "Alpha.Movie", "site_count": 3, "size": int64(100)},
			{"name": "Beta.Movie", "site_count": 3, "size": int64(200)},
			{"name": "Gamma.Movie", "site_count": 3, "size": int64(50)},
		}
		if reversed {
			items[0], items[2] = items[2], items[0]
		}
		return items
	}

	asc := build(false)
	service.sortData(asc, "site_count", "ascending")
	desc := build(true)
	service.sortData(desc, "site_count", "ascending")

	namesOf := func(items []map[string]any) []string {
		out := make([]string, len(items))
		for i, item := range items {
			out[i] = item["name"].(string)
		}
		return out
	}
	a := namesOf(asc)
	b := namesOf(desc)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("同值排序不稳定：正序输入=%v，逆序输入=%v", a, b)
		}
	}
	// 期望按名称确定性排序
	want := []string{"Alpha.Movie", "Beta.Movie", "Gamma.Movie"}
	for i := range want {
		if a[i] != want[i] {
			t.Fatalf("排序结果 = %v, want %v", a, want)
		}
	}
}

// TestSortDataDescendingWithTies 降序时同值仍按名称升序（确定性）。
func TestSortDataDescendingWithTies(t *testing.T) {
	service := &TorrentDataService{}
	items := []map[string]any{
		{"name": "B.Movie", "site_count": 5, "size": int64(100)},
		{"name": "A.Movie", "site_count": 5, "size": int64(200)},
		{"name": "C.Movie", "site_count": 9, "size": int64(50)},
	}
	service.sortData(items, "site_count", "descending")

	if items[0]["name"] != "C.Movie" {
		t.Fatalf("site_count 最大的应排第一，got %v", items[0]["name"])
	}
	if items[1]["name"] != "A.Movie" || items[2]["name"] != "B.Movie" {
		t.Fatalf("同值应按名称升序，got %v, %v", items[1]["name"], items[2]["name"])
	}
}
