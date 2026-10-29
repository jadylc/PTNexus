package fetch

import "testing"

// TestCollectSearchCandidatesOurBitsPage 用 OurBits 真实搜索结果页的结构片段回归：
// 页面含"热门海报"区块（10 个全站固定 details 链接）、用户信息区（userdetails 链接）、
// 搜索结果表格（唯一目标 id=230550）。修复前全页扫描得到 ~12 个候选（海报+用户链接+目标），
// 修复后应只有 1 个候选（目标）。
func TestCollectSearchCandidatesOurBitsPage(t *testing.T) {
	html := `
<html><body>
<table class="mainouter"><tbody><tr><td>
<table width="100%" border="1" cellspacing="0" cellpadding="5"><tbody><tr><td>
<a href="details.php?id=359500&amp;hit=1" onmouseover="domTT_activate(this, event, 'content', 'Mutiny 2026 UHD BluRay 2160p HEVC Atmos TrueHD7 1-BHYS@OurBits');"><img src="//cdn/poster/36889088.jpg"></a>
<a href="details.php?id=359248&amp;hit=1"><img src="//cdn/poster/3262044.jpg"></a>
<a href="details.php?id=358977&amp;hit=1"><img src="//cdn/poster/1981242.jpg"></a>
<a href="details.php?id=359561&amp;hit=1"><img src="//cdn/poster/36238849.jpg"></a>
<a href="details.php?id=358817&amp;hit=1"><img src="//cdn/poster/6887029.jpg"></a>
<a href="details.php?id=358616&amp;hit=1"><img src="//cdn/poster/1298040.jpg"></a>
<a href="details.php?id=359316&amp;hit=1"><img src="//cdn/poster/26339213.jpg"></a>
<a href="details.php?id=359393&amp;hit=1"><img src="//cdn/poster/1296992.jpg"></a>
<a href="details.php?id=358599&amp;hit=1"><img src="//cdn/poster/1296629.jpg"></a>
<a href="details.php?id=359659&amp;hit=1"><img src="//cdn/poster/1476023.jpg"></a>
</td></tr></tbody></table>
<table id="info_block"><tbody><tr><td>
<span class="medium">欢迎回来, <span class="nowrap"><a href="userdetails.php?id=61520" class="PowerUser_Name"><b>lc121a</b></a></span>
[<a onclick="return confirm();" href="logout.php?token=xxx">退出</a>]
[<a href="torrents.php?inclbookmarked=1&amp;allsec=1&amp;incldead=0">收藏</a>]
</td></tr></tbody></table>
<form method="get" name="searchbox" action="?">
<table class="searchbox"><tbody><tr><td>
<input id="searchinput" name="search" type="text" value="一兆游戏.Trillion.Game.S01.2023.1080p.friDay.WEB-DL.H264.AAC-OurTV">
</td></tr></tbody></table>
</form>
<table class="torrents" cellspacing="0" cellpadding="5" width="100%" id="torrenttable">
<tbody><tr>
<td class="colhead"><a href="?search=xxx&amp;sort=1&amp;type=asc">标题</a></td>
</tr>
<tr class="sticky_blank">
<td class="rowfollow" width="100%" align="left"><table class="torrentname" width="100%"><tbody><tr class="sticky_blank"><td class="embedded"><a title="Trillion Game S01 2023 1080p friDay WEB-DL H264 AAC-OurTV" href="details.php?id=230550&amp;hit=1"><b>Trillion Game S01 2023 1080p friDay WEB-DL H264 AAC-OurTV</b></a> <img class="pro_50pctdown" src="pic/trans.gif" alt="50%" title="50%"><br><div>日剧 | 一兆游戏 / 一兆$游戏 全10集</div></td><td class="embedded"><a id="rssmark0" href="javascript:rssmark(230550,0);"><img width="16px" src="pic/rss.png"></a></td><td><a href="download.php?id=230550"><img class="download" src="pic/trans.gif"></a></td></tr></tbody></table></td>
<td class="rowfollow" align="center"><b><a href="details.php?id=230550&amp;hit=1&amp;dllist=1#seeders">8</a></b></td>
</tr></tbody></table>
</td></tr></tbody></table>
</body></html>`

	candidates := collectSearchCandidates(html)
	if len(candidates) != 1 {
		t.Fatalf("expected exactly 1 candidate (the real search result), got %d: %s", len(candidates), candidatePreview(candidates))
	}
	if candidates[0].id != "230550" {
		t.Fatalf("expected candidate id 230550, got %q", candidates[0].id)
	}
	if candidates[0].title != "Trillion Game S01 2023 1080p friDay WEB-DL H264 AAC-OurTV" {
		t.Fatalf("unexpected title %q", candidates[0].title)
	}
}

// TestCollectSearchCandidatesNoTableFallback 找不到结果表格标记时回退全页解析
// （兼容非标准结构，宁可多候选也不丢真实结果）。
func TestCollectSearchCandidatesNoTableFallback(t *testing.T) {
	html := `<div><a title="Some Release" href="details.php?id=12345">Some Release</a></div>`
	candidates := collectSearchCandidates(html)
	if len(candidates) != 1 || candidates[0].id != "12345" {
		t.Fatalf("expected fallback full-page parse to find id 12345, got %v", candidates)
	}
}
