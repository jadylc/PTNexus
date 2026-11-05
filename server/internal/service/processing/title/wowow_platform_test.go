package title

import (
	"strings"
	"testing"
)

// TestWOWOWPlatformRecognized 日本电视台 WOWOW 应识别为片源平台，
// 不再残留进"无法识别"（hdhome/601485 case：Influence S01 2021 1080p WOWOW WEB-DL）。
func TestWOWOWPlatformRecognized(t *testing.T) {
	components := BuildSimpleTitleComponentsWithMediaInfo(
		"Influence S01 2021 1080p WOWOW WEB-DL AAC2.0 H.264-HHWEB", "", "")
	values := map[string]string{}
	for _, item := range components {
		key := strings.TrimSpace(toTestString(item["key"]))
		values[key] = strings.TrimSpace(toTestString(item["value"]))
	}
	if values["片源平台"] != "WOWOW" {
		t.Fatalf("expected 片源平台=WOWOW, got %q", values["片源平台"])
	}
	if values["无法识别"] != "" {
		t.Fatalf("expected 无法识别 empty, got %q", values["无法识别"])
	}
}
