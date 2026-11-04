package repair

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEnsureImageWithinUploadLimit 用 ffmpeg 生成一张超限大图，验证逐级压缩能降到目标以内。
// 环境缺少 ffmpeg 时跳过（CI 无该依赖）。
func TestEnsureImageWithinUploadLimit(t *testing.T) {
	ffmpegPath, err := resolveBinary("ffmpeg", "PTNEXUS_FFMPEG_PATH")
	if err != nil {
		t.Skip("ffmpeg 不可用，跳过压缩守卫测试")
	}

	tmp := t.TempDir()
	src := filepath.Join(tmp, "oversize.png")
	// 高噪声 4K 帧：PNG 最难压的内容，逼近真实最坏情况
	cmd := exec.Command(ffmpegPath,
		"-y", "-v", "error",
		"-f", "lavfi", "-i", "nullsrc=size=3840x2160:rate=1",
		"-frames:v", "1",
		"-vf", "noise=alls=100:allf=t,format=rgb24",
		"-compression_level", "0",
		src,
	)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Skipf("生成测试图失败: %v %s", runErr, out)
	}
	info, statErr := os.Stat(src)
	if statErr != nil {
		t.Fatalf("stat 失败: %v", statErr)
	}
	if info.Size() <= uploadSizeLimitBytes {
		t.Skipf("测试图仅 %d 字节，未超过目标，无法触发压缩", info.Size())
	}

	got := EnsureImageWithinUploadLimit(src, func(string, ...any) {})
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("结果文件不可读: %v", err)
	}
	if gotInfo.Size() > uploadSizeLimitBytes {
		t.Fatalf("压缩后仍超限: %d > %d (path=%s)", gotInfo.Size(), uploadSizeLimitBytes, got)
	}
	if got == src {
		t.Fatalf("预期返回压缩产物路径，实际返回原路径")
	}
	t.Logf("压缩成功: %.1f MB -> %.1f MB (%s)",
		float64(info.Size())/1024/1024,
		float64(gotInfo.Size())/1024/1024,
		filepath.Base(got))
}

// TestEnsureImageWithinUploadLimitKeepsSmallFile 小文件不应触发任何压缩。
func TestEnsureImageWithinUploadLimitKeepsSmallFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "small.png")
	if err := os.WriteFile(src, make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := EnsureImageWithinUploadLimit(src, nil); got != src {
		t.Fatalf("小文件应返回原路径, got %s", got)
	}
}
