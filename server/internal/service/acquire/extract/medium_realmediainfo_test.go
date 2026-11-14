package extract

import "testing"

// TestInferMediumWebDLWithRealMediaInfo 验证 WEB-DL 种子的典型 MediaInfo
// 不会把 medium 推成 bluray/encode。
func TestInferMediumWebDLWithRealMediaInfo(t *testing.T) {
	// 典型 WEB-DL MediaInfo（Matroska + HEVC + EAC3）
	mi := `General
Unique ID : 1234567890 (0x1234567890)
Complete name : Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB.mkv
Format : Matroska
Format version : Version 4
File size : 18.4 GiB
Duration : 45 min 12 s
Overall bit rate : 8 000 kb/s

Video
Format : AVC
Format/Info : Advanced Video Codec
Format profile : High@L4
Width : 1 920 pixels
Height : 1 080 pixels

Audio
Format : E-AC-3
Format/Info : Enhanced AC-3
Channel(s) : 6 channels
Language : English`

	hints := "Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB " +
		"电视剧 WEB-DL WEB-DL AVC/H264/x264 DDP/EAC3 1080p WEB-DL HHWEB"

	values := InferStandardizedValues(hints, mi, "")
	if got := values["medium"]; got != "medium.webdl" {
		t.Fatalf("medium=%q, want medium.webdl", got)
	}
}
