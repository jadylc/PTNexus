package title

import (
	"strings"
	"testing"
)

// TestBuildSimpleTitleComponentsHDR10Plus 验证 HDR10+ 被识别进 HDR 格式后，
// 不再泄漏进「无法识别」。
// 背景：移除已识别标签用 \b...\b 正则，而 HDR10+ 以非单词字符 '+' 结尾，
// 后面接 '.' 或空格时尾部 \b 不成立，导致标签移除失败、泄漏进"无法识别"。
// 注意：制作组后缀残留（-GROUP）属于既有行为，不属于本修复范围；
// 断言只检查 HDR10+ 本身不出现在"无法识别"里。
func TestBuildSimpleTitleComponentsHDR10Plus(t *testing.T) {
	title := "Show.Name.S01.2160p.AMZN.WEB-DL.DDP.5.1.Atmos.HDR10+.H.265-MyGroup"
	components := BuildSimpleTitleComponents(title, "MyGroup")

	hdr := componentValue(components, "HDR格式")
	if hdr != "HDR10+" {
		t.Fatalf("expected HDR格式=HDR10+, got %q", hdr)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(unrecognized, "HDR10+") {
		t.Fatalf("expected HDR10+ NOT in 无法识别, got %q", unrecognized)
	}
}

// TestBuildSimpleTitleComponentsHDR10PlusDotBoundary 变体：HDR10+. 后跟点号（常见写法 HDR10+.HEVC）。
func TestBuildSimpleTitleComponentsHDR10PlusDotBoundary(t *testing.T) {
	title := "Movie.2024.2160p.UHD.BluRay.Remux.HDR10+.HEVC-GROUP"
	components := BuildSimpleTitleComponents(title, "GROUP")

	if got := componentValue(components, "HDR格式"); got != "HDR10+" {
		t.Fatalf("expected HDR格式=HDR10+, got %q", got)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(unrecognized, "HDR10+") {
		t.Fatalf("expected HDR10+ NOT in 无法识别, got %q", unrecognized)
	}
}

// TestBuildSimpleTitleComponentsHDRPlusNoGroup 验证不带制作组且标题无连字符时：
// DoVi+HDR10+ 组合正常识别进 HDR 格式，且 HDR10+ 不泄漏进"无法识别"。
// 注意：标题含 WEB-DL 等 dash 时会被 Pythonish 规则切分制作组（既有行为），故此处避开；
// 独立 Atmos 无音频编码前缀时不会被音频清理覆盖（既有范围），断言只检查 HDR10+ 不泄漏。
func TestBuildSimpleTitleComponentsHDRPlusNoGroup(t *testing.T) {
	title := "Movie.2024.2160p.UHD.BluRay.DoVi.HDR10+.HEVC.Atmos"
	components := BuildSimpleTitleComponents(title, "")

	if got := componentValue(components, "HDR格式"); got != "DoVi HDR10+" {
		t.Fatalf("expected HDR格式=DoVi HDR10+, got %q", got)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(unrecognized, "HDR10+") {
		t.Fatalf("expected HDR10+ NOT in 无法识别, got %q", unrecognized)
	}
}

func componentValue(components []map[string]any, key string) string {
	for _, item := range components {
		if k, ok := item["key"].(string); ok && k == key {
			if v, ok := item["value"].(string); ok {
				return v
			}
			return ""
		}
	}
	return ""
}

// TestBuildSimpleTitleComponentsPMTPPlatform 验证 Paramount+ 场景缩写 PMTP 被识别为片源平台，
// 且不泄漏进"无法识别"（站点原始标题：Limitless.2015.Complete.PMTP.WEB-DL.1080p.H264.AAC-CMCTV）。
func TestBuildSimpleTitleComponentsPMTPPlatform(t *testing.T) {
	title := "Limitless.2015.Complete.PMTP.WEB-DL.1080p.H264.AAC-CMCTV"
	components := BuildSimpleTitleComponents(title, "CMCTV")

	if got := componentValue(components, "片源平台"); got != "PMTP" {
		t.Fatalf("expected 片源平台=PMTP, got %q", got)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(unrecognized, "PMTP") {
		t.Fatalf("expected PMTP NOT in 无法识别, got %q", unrecognized)
	}
}

// TestBuildSimpleTitleComponentsHDTVRip 验证 HDTVRip 被识别为媒介 HDTV，
// 且不再泄漏进"无法识别"。
// 背景：\bHDTV\b 被后缀 Rip 的词边界挡住、\bTV[-\s]?RIP\b 又要求前面是词边界，
// 两个正则都匹配不到 HDTVRip，导致媒介为空、token 落进无法识别。
func TestBuildSimpleTitleComponentsHDTVRip(t *testing.T) {
	title := "Yonimo Kimyouna Monogatari 23 Aki no Tokubetsuhen 2023 720p HDTVRip AAC 2.0 x264-TVBT"
	components := BuildSimpleTitleComponents(title, "TVBT")

	if got := componentValue(components, "媒介"); got != "HDTV" {
		t.Fatalf("expected 媒介=HDTV, got %q", got)
	}
	unrecognized := componentValue(components, "无法识别")
	if strings.Contains(strings.ToUpper(unrecognized), "HDTVRIP") {
		t.Fatalf("expected HDTVRip NOT in 无法识别, got %q", unrecognized)
	}
}

// TestBuildSimpleTitleComponentsHDTVRipVariants 覆盖连字符/空格写法。
func TestBuildSimpleTitleComponentsHDTVRipVariants(t *testing.T) {
	cases := []string{
		"Show Name 2023 720p HDTV-Rip AAC x264-GRP",
		"Show Name 2023 720p HDTV Rip AAC x264-GRP",
	}
	for _, title := range cases {
		components := BuildSimpleTitleComponents(title, "GRP")
		if got := componentValue(components, "媒介"); got != "HDTV" {
			t.Fatalf("title %q: expected 媒介=HDTV, got %q", title, got)
		}
		unrecognized := componentValue(components, "无法识别")
		if strings.Contains(strings.ToUpper(unrecognized), "HDTV") {
			t.Fatalf("title %q: expected HDTV token NOT in 无法识别, got %q", title, unrecognized)
		}
	}
}
