package extract

import "testing"

// TestMediaInfoEncodingSettingsBlurayCompat 复现 2026.11 实测根因：
// x264 的 MediaInfo「Encoding settings」行含 bluray_compat=0，
// 其中的 "bluray" 字样被 medium 推断当成蓝光标记 → medium.bluray
// → finalize 按「bluray + 有 MediaInfo = 压片」改成 medium.encode。
// 这是所有 x264/x265 源种（MediaInfo 必带 Encoding settings）的通病。
func TestMediaInfoEncodingSettingsBlurayCompat(t *testing.T) {
	mi := `General
Complete name : Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB.mkv
Format : Matroska
File size : 18.4 GiB
Duration : 45 min 12 s
Overall bit rate : 8 000 kb/s

Video
Format : AVC
Width : 1 920 pixels
Height : 1 080 pixels
Encoding settings : cabac=1 / ref=3 / deblock=1:0:0 / analyse=0x1:0x111 / me=umh / subme=10 / psy=1 / mixed_ref=1 / chroma_me=1 / trellis=2 / 8x8dct=0 / cqm=0 / deadzone=21,11 / fast_pskip=1 / chroma_qp_offset=-2 / threads=6 / lookahead_threads=1 / sliced_threads=0 / nr=0 / decimate=1 / interlaced=0 / bluray_compat=0 / stitchable=1 / constrained_intra=0 / bframes=2 / b_pyramid=0 / b_adapt=2 / b_bias=0 / direct=3 / weightb=1 / open_gop=0 / weightp=2 / keyint=250 / keyint_min=126 / scenecut=0 / intra_refresh=0 / rc_lookahead=50 / rc=2pass / mbtree=1 / bitrate=5000 / ratetol=1.0 / qcomp=0.50 / qpmin=6 / qpmax=51 / qpstep=4 / cplxblur=20.0 / qblur=0.5 / vbv_maxrate=7500 / vbv_bufsize=10000 / nal_hrd=vbr / filler=0 / ip_ratio=1.40 / aq=1:1.00
Language : English

Audio
Format : E-AC-3
Channel(s) : 6 channels`

	title := "Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB"
	values := InferStandardizedValues(title, mi, "")

	if got := values["medium"]; got != "medium.webdl" {
		t.Fatalf("medium=%q, want medium.webdl（Encoding settings 的 bluray_compat 不应污染媒介推断）", got)
	}
}

// TestEncodingSettingsSanitizedForInference 验证清洗后 Encoding settings 行被去除。
func TestEncodingSettingsSanitizedForInference(t *testing.T) {
	mi := "Video\nFormat : AVC\nEncoding settings : cabac=1 / bluray_compat=0 / bitrate=5000\nLanguage : English"
	sanitized := SanitizeMediaTextForAnalysis(mi)
	if containsIgnoreCase(sanitized, "bluray") {
		t.Fatalf("清洗后仍含 bluray 字样，sanitized=%q", sanitized)
	}
}

func containsIgnoreCase(s, sub string) bool {
	lower := ""
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			lower += string(r + 32)
		} else {
			lower += string(r)
		}
	}
	target := ""
	for _, r := range sub {
		if r >= 'A' && r <= 'Z' {
			target += string(r + 32)
		} else {
			target += string(r)
		}
	}
	for i := 0; i+len(target) <= len(lower); i++ {
		if lower[i:i+len(target)] == target {
			return true
		}
	}
	return false
}
