package extract

import "testing"

// TestInferMediumWebDLFromRealTitle 回归 2026.11：真实种子标题
// Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB
// 被识别成 medium.encode，应为 medium.webdl。
func TestInferMediumWebDLFromRealTitle(t *testing.T) {
	cases := []string{
		"Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB",
		"[CBG].Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB",
		"Giri & Haji S01 2019 1080p NF WEB-DL H.264 DDP 5.1 Atmos-HHWEB",
	}
	for _, title := range cases {
		values := InferStandardizedValues(title, "", "")
		if got := values["medium"]; got != "medium.webdl" {
			t.Errorf("title=%q medium=%q, want medium.webdl", title, got)
		}
	}
}

// TestInferMediumEncodeStillWorks 真正需要 encode 的场景不受影响。
func TestInferMediumEncodeStillWorks(t *testing.T) {
	values := InferStandardizedValues("Movie.2020.1080p.BluRay.x264-ENCODE", "", "")
	if got := values["medium"]; got == "" {
		t.Errorf("medium 不应为空")
	}
}
