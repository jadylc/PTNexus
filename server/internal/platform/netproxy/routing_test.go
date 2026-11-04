package netproxy

import (
	"testing"

	"github.com/pt-nexus/server/internal/config"
)

func TestClassifyHost(t *testing.T) {
	cases := []struct {
		host string
		want hostCategory
	}{
		{"api.themoviedb.org", categoryMedia},
		{"image.tmdb.org", categoryMedia},
		{"www.imdb.com", categoryMedia},
		{"movie.douban.com", categoryMedia},
		{"cspt.top", categoryMedia},
		{"ptgen.homeqian.top", categoryMedia},
		{"ptgen.tju.pt", categoryMedia},
		{"api.iyuu.cn", categoryIYUU},
		{"2025.iyuu.cn", categoryIYUU},
		{"api.pixhost.to", categoryImageHost},
		{"img3.pixhost.to", categoryImageHost},
		{"pt-nexus-proxy.1395251710.workers.dev", categoryImageHost},
		{"pt-nexus-ptgen.1395251710.workers.dev", categoryMedia},
		{"pt-nexus-imdb2douban.1395251710.workers.dev", categoryMedia},
		{"hdfans.org", categoryOther},
	}
	for _, item := range cases {
		if got := classifyHost(item.host); got != item.want {
			t.Fatalf("classifyHost(%s) = %d, want %d", item.host, got, item.want)
		}
	}
}

func TestHostUsesProxyCategories(t *testing.T) {
	categories := config.NetworkProxyCategories{Media: true, ImageHost: false, IYUU: false, Other: false}
	if !hostUsesProxy("api.themoviedb.org", categories, nil) {
		t.Fatalf("expected media host to use proxy when media=true")
	}
	if hostUsesProxy("api.pixhost.to", categories, nil) {
		t.Fatalf("expected image host to bypass proxy when image=false")
	}
	if hostUsesProxy("hdfans.org", categories, nil) {
		t.Fatalf("expected PT site to bypass proxy when other=false")
	}
}

func TestHostUsesProxySiteWhitelist(t *testing.T) {
	categories := config.DefaultNetworkProxyCategories()
	sites := []string{"hdfans.org", ".pterclub.com"}
	if !hostUsesProxy("hdfans.org", categories, sites) {
		t.Fatalf("expected whitelisted site to use proxy")
	}
	if !hostUsesProxy("www.pterclub.com", categories, sites) {
		t.Fatalf("expected subdomain of whitelisted site to use proxy")
	}
	if hostUsesProxy("kame-gpt.club", categories, sites) {
		t.Fatalf("expected non-whitelisted site to bypass proxy")
	}
}

func TestMatchDomainAcceptsURLPattern(t *testing.T) {
	if !matchDomain("hdfans.org", "https://hdfans.org/") {
		t.Fatalf("expected URL pattern to normalize to host")
	}
	if matchDomain("notthdfans.org", "hdfans.org") {
		t.Fatalf("expected suffix match to require domain boundary")
	}
}
