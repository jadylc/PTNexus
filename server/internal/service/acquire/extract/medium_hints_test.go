package extract

import "testing"

// TestInferMediumFromHHanClubHints 复现憨憨提取器的 hints 拼装后推断：
// 详情页 媒介=WEB-DL / 视频编码=H.264 / 音频编码=DDP 5.1 Atmos / 来源=WEB-DL
// 拼成的 hints 交给 InferStandardizedValues，medium 必须仍是 webdl。
func TestInferMediumFromHHanClubHints(t *testing.T) {
	// 模拟憨憨提取器 hints 的实际拼接（类型+媒介+视频+音频+分辨率+来源+制作组+标题）
	hints := "电视剧 WEB-DL H.264 DDP 5.1 Atmos 1080p WEB-DL HHWEB " +
		"Giri.&.Haji.S01.2019.1080p.NF.WEB-DL.DDP5.1.Atmos.H264-HHWEB"

	values := InferStandardizedValues(hints, "", "")
	if got := values["medium"]; got != "medium.webdl" {
		t.Fatalf("hints 推断 medium=%q, want medium.webdl（hints=%q）", got, hints)
	}
}
