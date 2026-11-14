package persist

import (
	"strings"
	"testing"
)

// TestCheckSeedMaintainabilityOnlyIntroAndMediainfo 校验口径回归：
// 只校验简介与媒体信息两项——标题含"无法识别"、海报/截图/标准参数缺失、
// 制度组缺失等均不应拦截（2026.11 用户明确要求）。
func TestCheckSeedMaintainabilityOnlyIntroAndMediainfo(t *testing.T) {
	row := map[string]any{
		// 标题含无法识别、无海报无截图、标准参数全空——都不该拦
		"title_components":    `[{"key":"无法识别","value":"DSNP"}]`,
		"poster":              "",
		"screenshots":         "",
		"standardized_params": map[string]any{},
		"body":                "◎片　　名　Test\n◎产　　地　美国\n◎简介　test synopsis",
		"mediainfo":           "General\nFormat : Matroska\nFile size : 1 GiB\nDuration : 1h\nOverall bit rate : 1 Mb/s\nVideo\nFormat : HEVC\nWidth : 1 pixels\nHeight : 1 pixels\nAudio\nFormat : E-AC-3\nLanguage : en",
	}

	problems := CheckSeedMaintainability(row)
	if len(problems) != 0 {
		t.Fatalf("只该校验简介与媒体信息，其他项不应拦截，实际问题: %#v", problems)
	}
}

// TestCheckSeedMaintainabilityIntroMissingField 简介缺关键字段时拦截。
func TestCheckSeedMaintainabilityIntroMissingField(t *testing.T) {
	row := map[string]any{
		"body":      "这是一段没有片名产地简介字段的文字",
		"mediainfo": "General\nFormat : Matroska\nFile size : 1 GiB\nDuration : 1h\nOverall bit rate : 1 Mb/s\nVideo\nFormat : HEVC\nWidth : 1 pixels\nHeight : 1 pixels\nAudio\nFormat : E-AC-3\nLanguage : en",
	}
	problems := CheckSeedMaintainability(row)
	if len(problems) == 0 {
		t.Fatalf("简介缺关键字段应拦截")
	}
	joined := strings.Join(problems, "；")
	if !strings.Contains(joined, "简介缺少关键字段") {
		t.Fatalf("期望简介相关提示，实际: %#v", problems)
	}
}

// TestCheckSeedMaintainabilityMediainfoInvalid 媒体信息为空或格式无效时拦截。
func TestCheckSeedMaintainabilityMediainfoInvalid(t *testing.T) {
	body := "◎片　　名　Test\n◎产　　地　美国\n◎简介　test synopsis"

	empty := CheckSeedMaintainability(map[string]any{"body": body})
	if len(empty) == 0 || !strings.Contains(strings.Join(empty, "；"), "媒体信息为空") {
		t.Fatalf("媒体信息为空应拦截，实际: %#v", empty)
	}

	invalid := CheckSeedMaintainability(map[string]any{"body": body, "mediainfo": "这不是媒体信息"})
	if len(invalid) == 0 || !strings.Contains(strings.Join(invalid, "；"), "媒体信息格式无效") {
		t.Fatalf("媒体信息格式无效应拦截，实际: %#v", invalid)
	}
}
