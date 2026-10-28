package migrationflow

import (
	"testing"

	"github.com/pt-nexus/server/internal/service/reversemapping"
)

// TestRepairRowTagsAtmosRemoval tag.Atmos 是音频特性误存为标签：重算应移除。
func TestRepairRowTagsAtmosRemoval(t *testing.T) {
	reverseMappings := reversemapping.Build(nil)
	row := map[string]any{
		"tags": `["tag.中字","tag.Atmos","tag.剧情"]`,
	}
	fixed, changed := repairRowTags(row, "hdsky", reverseMappings)
	if !changed {
		t.Fatalf("expected tags changed")
	}
	for _, tag := range fixed {
		if tag == "tag.Atmos" {
			t.Fatalf("expected tag.Atmos removed, got %v", fixed)
		}
	}
	if len(fixed) != 2 {
		t.Fatalf("expected 2 tags remain, got %v", fixed)
	}
}

// TestRepairRowTagsKeepsValid 有效标签不动。
func TestRepairRowTagsKeepsValid(t *testing.T) {
	reverseMappings := reversemapping.Build(nil)
	row := map[string]any{
		"tags": `["tag.中字","tag.剧情"]`,
	}
	_, changed := repairRowTags(row, "hdsky", reverseMappings)
	if changed {
		t.Fatalf("expected no change for valid tags")
	}
}

// TestRepairRowTagsDedup 重复标签去重。
func TestRepairRowTagsDedup(t *testing.T) {
	reverseMappings := reversemapping.Build(nil)
	row := map[string]any{
		"tags": `["tag.中字","tag.中字"]`,
	}
	fixed, changed := repairRowTags(row, "hdsky", reverseMappings)
	if !changed || len(fixed) != 1 {
		t.Fatalf("expected dedup to 1 tag, changed=%t fixed=%v", changed, fixed)
	}
}
