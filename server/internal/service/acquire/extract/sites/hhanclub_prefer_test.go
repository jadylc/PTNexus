package sites

import "testing"

// TestHHanClubMediumNotOverriddenByInference 复现 2026.11 实测：
// 详情页「媒介」明确是 WEB-DL，但 MediaInfo/标题里出现 BluRay 类字样时，
// 推断结果 medium.bluray 会覆盖站点值，再被 NormalizeMediumByMediaType
// 按「含 BLU + 有 MediaInfo → 压片」改成 medium.encode。
func TestHHanClubMediumNotOverriddenByInference(t *testing.T) {
	// 站点明确值
	siteMedium := "medium.webdl"
	// 推断结果（假设 MediaInfo 或简介里含 BluRay 字样）
	inferredMedium := "medium.bluray"

	got := preferHHanClubValue(siteMedium, inferredMedium, "medium.other")
	if got != "medium.webdl" {
		t.Fatalf("站点媒介 WEB-DL 被推断值覆盖为 %q（期望 medium.webdl）", got)
	}
}
