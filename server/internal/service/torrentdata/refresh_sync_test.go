package torrentdata

import (
	"testing"

	"github.com/pt-nexus/server/internal/repository"
)

func TestRefreshSiteMatcherPrefersExactHostOverSharedCore(t *testing.T) {
	matcher := newRefreshSiteMatcher([]repository.SiteIdentity{
		{Nickname: "馒头", Site: "m-team", BaseURL: "kp.m-team.cc", SpecialTrackerDomain: "manfuz"},
		{Nickname: "我堡", Site: "ourbits", BaseURL: "ourbits.club"},
	})

	got := matcher.Match(
		[]string{"https://tracker.m-team.cc/announce"},
		"https://ourbits.club/details.php?id=123",
		"https://ourbits.club/details.php?id=123",
	)
	if got != "我堡" {
		t.Fatalf("expected 我堡, got %q", got)
	}
}

// TestRefreshSiteMatcherBareWordSpecialDomain special_tracker_domain 逗号分隔多域名的
// 裸词（无 TLD）通过子域前缀匹配：彩虹岛 tracker（tracker.rainbowisland.co）应识别为
// 彩虹岛——此前整串当单个 host 存表，导致该站种子站点识别失败（sites 为空，
// 一种多站做种数显示 0/98）。冲突裸词（多站点共用同词）不参与匹配。
func TestRefreshSiteMatcherBareWordSpecialDomain(t *testing.T) {
	matcher := newRefreshSiteMatcher([]repository.SiteIdentity{
		{Nickname: "彩虹岛", Site: "chdbits", BaseURL: "ptchdbits.co", SpecialTrackerDomain: "rainbowisland,ptchdbits,chdbits"},
		{Nickname: "猫站", Site: "pterclub", BaseURL: "pterclub.net"},
	})

	// 彩虹岛 tracker：裸词 rainbowisland 以子域前缀命中
	got := matcher.Match([]string{"https://tracker.rainbowisland.co/announce"}, "", "")
	if got != "彩虹岛" {
		t.Fatalf("expected 彩虹岛, got %q", got)
	}
	// 旧面域名 ptchdbits.co 精确命中
	if got := matcher.Match([]string{"https://ptchdbits.co/announce"}, "", ""); got != "彩虹岛" {
		t.Fatalf("expected 彩虹岛 via ptchdbits.co, got %q", got)
	}
	// 猫站不受影响
	if got := matcher.Match([]string{"https://pterclub.net/announce"}, "", ""); got != "猫站" {
		t.Fatalf("expected 猫站, got %q", got)
	}

	// 冲突裸词（两个站点配同一个裸词）不参与匹配
	conflict := newRefreshSiteMatcher([]repository.SiteIdentity{
		{Nickname: "站A", Site: "a", BaseURL: "a.com", SpecialTrackerDomain: "shared"},
		{Nickname: "站B", Site: "b", BaseURL: "b.com", SpecialTrackerDomain: "shared"},
	})
	if got := conflict.Match([]string{"https://tracker.shared.co/announce"}, "", ""); got == "站A" || got == "站B" {
		t.Fatalf("conflicting bare word should not match, got %q", got)
	}
}

func TestExtractCoreDomainKeepsRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"kp.m-team.cc":      "m-team.cc",
		"tracker.m-team.cc": "m-team.cc",
		"ourbits.club":      "ourbits.club",
	}
	for input, want := range cases {
		if got := extractCoreDomain(input); got != want {
			t.Fatalf("extractCoreDomain(%q) = %q, want %q", input, got, want)
		}
	}
}
