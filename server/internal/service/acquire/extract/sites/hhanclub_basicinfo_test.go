package sites

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// TestExtractHHanClubBasicInfoRealPage 用与用户实测一致的详情页基本信息结构验证解析：
// 类型=电视剧 来源=WEB-DL 媒介=WEB-DL 编码=AVC/H264/x264 音频编码=DDP/EAC3
// 分辨率=1080p 处理=英剧 制作组=HHWEB
func TestExtractHHanClubBasicInfoRealPage(t *testing.T) {
	const page = `<html><body>
<div class="basic-info-header">基本信息</div>
<div class="basic-info-body">
  <div><span>大小：</span><span>18.39 GB</span></div>
  <div><span>类型：</span><span>电视剧</span></div>
  <div><span>来源：</span><span>WEB-DL</span></div>
  <div><span>媒介：</span><span>WEB-DL</span></div>
  <div><span>编码：</span><span>AVC/H264/x264</span></div>
  <div><span>音频编码：</span><span>DDP/EAC3</span></div>
  <div><span>分辨率：</span><span>1080p</span></div>
  <div><span>处理：</span><span>英剧</span></div>
  <div><span>制作组：</span><span>HHWEB</span></div>
</div>
</body></html>`

	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	info := extractHHanClubBasicInfo(doc)

	if got := info["媒介"]; got != "WEB-DL" {
		t.Errorf("媒介 = %q, want WEB-DL（info=%#v）", got, info)
	}
	if got := info["来源"]; got != "WEB-DL" {
		t.Errorf("来源 = %q, want WEB-DL", got)
	}
	if got := info["处理"]; got != "英剧" {
		t.Errorf("处理 = %q, want 英剧", got)
	}
	if got := info["视频编码"]; got != "AVC/H264/x264" {
		t.Errorf("视频编码 = %q, want AVC/H264/x264", got)
	}
	if got := info["音频编码"]; got != "DDP/EAC3" {
		t.Errorf("音频编码 = %q, want DDP/EAC3", got)
	}
	if got := info["制作组"]; got != "HHWEB" {
		t.Errorf("制作组 = %q, want HHWEB", got)
	}

	// 媒介映射
	if got := mapHHanClubMedium(info["媒介"]); got != "medium.webdl" {
		t.Errorf("mapHHanClubMedium(%q) = %q, want medium.webdl", info["媒介"], got)
	}
}
