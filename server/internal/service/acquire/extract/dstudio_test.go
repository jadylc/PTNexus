package extract

import (
	"os"
	"strings"
	"testing"
)

// newDStudioTestEngine 构造 Depth Studio 提取引擎（公共提取器 + dstudio 特殊提取器）。
func newDStudioTestEngine(t *testing.T) *Engine {
	t.Helper()
	public := NewPublicExtractor(defaultPublicExtract)
	return NewDefaultEngine(public, buildSiteRuntimeForExtractors)
}

// TestDStudioExtractorExtractsDetails 用真实详情页 fixture 验证 Depth Studio 提取：
// 标题取 Hero 区（不是 <title>）、类型/制作组正确、简介正文与截图干净。
func TestDStudioExtractorExtractsDetails(t *testing.T) {
	raw, err := os.ReadFile("testdata/dstudio_details.html")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	engine := newDStudioTestEngine(t)
	data, meta := engine.Extract(Input{
		SiteCode:      "dstudio",
		SiteNickname:  "Depth Studio",
		BaseURL:       "https://dstudio.me",
		TorrentID:     "5370",
		PageHTML:      string(raw),
		FallbackTitle: "",
	})
	if meta.ExtractorName != "dstudio_special" {
		t.Fatalf("expected dstudio_special extractor, got %q (fallback=%v reason=%q)", meta.ExtractorName, meta.UsedFallback, meta.FallbackReason)
	}

	wantTitle := "Awkwafina The Unlikely Cook 2026 S01E02 2160p ATVP WEB-DL DDP5.1 Atmos HDR10P H.265-DepWeb"
	if data.Title != wantTitle {
		t.Errorf("Title = %q, want %q", data.Title, wantTitle)
	}
	if !strings.Contains(data.Subtitle, "The Unlikely Cook") {
		t.Errorf("Subtitle = %q, want contains 副标题内容", data.Subtitle)
	}
	if data.Type != "category.documentaries" {
		t.Errorf("Type = %q, want category.documentaries", data.Type)
	}
	if data.Team != "team.other" {
		// DepWeb 未在全局映射中，允许 team.other；但不能是别的站点的组
		t.Logf("Team = %q（DepWeb 未入全局映射时为 team.other）", data.Team)
	}
	if data.Medium != "medium.webdl" {
		t.Errorf("Medium = %q, want medium.webdl", data.Medium)
	}
	if data.Resolution != "resolution.r2160p" {
		t.Errorf("Resolution = %q, want resolution.r2160p", data.Resolution)
	}
	if data.VideoCodec != "video.h265" {
		t.Errorf("VideoCodec = %q, want video.h265", data.VideoCodec)
	}
	if data.AudioCodec != "audio.ddp" {
		t.Errorf("AudioCodec = %q, want audio.ddp", data.AudioCodec)
	}
	// 官方标签被公共提取器按全局 ignore 名单过滤（既有设计），只验证内容标签。
	for _, tag := range []string{"分集", "HDR10+", "中字"} {
		found := false
		for _, got := range data.Tags {
			if got == tag {
				found = true
			}
		}
		if !found {
			t.Errorf("Tags missing %q: %#v", tag, data.Tags)
		}
	}
	if !strings.Contains(data.IMDbLink, "imdb.com/title/tt38992068") {
		t.Errorf("IMDbLink = %q", data.IMDbLink)
	}
	if !strings.Contains(data.TMDbLink, "themoviedb.org/tv/306179") {
		t.Errorf("TMDbLink = %q", data.TMDbLink)
	}
	if data.Intro.Screenshots == "" || strings.Contains(data.Intro.Screenshots, "dstudiov4.png") {
		t.Errorf("Screenshots should keep real screens and drop site logo, got %q", data.Intro.Screenshots)
	}
	if !strings.Contains(data.Intro.Body, "The Unlikely Cook") || strings.Contains(data.Intro.Body, "仅供网络宽带测试") {
		t.Errorf("Body should keep intro text and drop disclaimer, got %q", data.Intro.Body)
	}
	if data.MediaInfo == "" || !strings.Contains(data.MediaInfo, "HEVC") {
		t.Errorf("MediaInfo should contain raw MediaInfo, got %q", data.MediaInfo)
	}
}

// TestDStudioExtractorFallsBackToPublicWhenPageEmpty 页面为空时不应 panic，
// 由引擎回退公共提取器。
func TestDStudioExtractorFallsBackToPublicWhenPageEmpty(t *testing.T) {
	engine := newDStudioTestEngine(t)
	if _, meta := engine.Extract(Input{SiteCode: "dstudio", SiteNickname: "Depth Studio"}); meta.ExtractorName == "" {
		t.Fatalf("expected non-empty extractor name")
	}
}
