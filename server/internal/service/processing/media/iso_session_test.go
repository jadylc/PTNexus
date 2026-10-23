package media

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildMediaPathCandidatesAllowsDirScanForNonMatchingSavePath(t *testing.T) {
	candidates := buildMediaPathCandidates(
		[]string{filepath.Join("downloads", "actual-dir")},
		"The.Fire.Raven.2025.2160p.WEB-DL.H265.HDR.DDP5.1-HHWEB",
		"The Fire Raven 2025 2160p WEB-DL H265 HDR DDP5.1-HHWEB",
	)

	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	last := candidates[2]
	if last.Source != "save_path" {
		t.Fatalf("expected last candidate to use save_path source, got %s", last.Source)
	}
	if !last.AllowDirScan {
		t.Fatalf("expected save_path candidate to allow dir scan")
	}
}

func TestBuildMediaPathCandidatesPreservesPriorityOrder(t *testing.T) {
	savePath := filepath.Join("downloads", "root")
	torrentName := "Movie.2024.2160p"
	contentName := "Movie"

	candidates := buildMediaPathCandidates([]string{savePath}, torrentName, contentName)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	if got := candidates[0].Path; got != filepath.Join(savePath, torrentName) {
		t.Fatalf("unexpected torrent candidate path: %s", got)
	}
	if got := candidates[1].Path; got != filepath.Join(savePath, contentName) {
		t.Fatalf("unexpected content candidate path: %s", got)
	}
	if got := candidates[2].Path; got != savePath {
		t.Fatalf("unexpected save_path candidate path: %s", got)
	}
}

func TestBuildMediaPathCandidatesHandlesSingleFileSavePath(t *testing.T) {
	savePath := filepath.Join("downloads", "movie.mkv")
	candidates := buildMediaPathCandidates([]string{savePath}, "", "")
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Path != savePath {
		t.Fatalf("unexpected candidate path: %s", candidates[0].Path)
	}
	if !candidates[0].AllowDirScan {
		t.Fatalf("expected save_path candidate to allow dir scan")
	}
}

func TestFindMediaSiblingByBaseNameMatchesSingleMediaFile(t *testing.T) {
	tempDir := t.TempDir()
	base := "Sleepwalkers.2021.BluRay.1080p.DD5.1.x264-BMDru"
	matchedPath := filepath.Join(tempDir, base+".mkv")
	if err := os.WriteFile(matchedPath, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write matched file: %v", err)
	}

	resolved, err := findMediaSiblingByBaseName(filepath.Join(tempDir, base), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != matchedPath {
		t.Fatalf("expected %s, got %s", matchedPath, resolved)
	}
}

func TestFindMediaSiblingByBaseNameRejectsMultipleMatches(t *testing.T) {
	tempDir := t.TempDir()
	base := "Sleepwalkers.2021.BluRay.1080p.DD5.1.x264-BMDru"
	for _, ext := range []string{".mkv", ".mp4"} {
		if err := os.WriteFile(filepath.Join(tempDir, base+ext), []byte("ok"), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
	}

	if _, err := findMediaSiblingByBaseName(filepath.Join(tempDir, base), true); err == nil {
		t.Fatalf("expected multiple match error")
	}
}

// writeMediaTestFile 按指定大小写出占位媒体文件，用于构造"邻居文件更大"的场景。
func writeMediaTestFile(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestSavePathLooksLikeReleaseDir(t *testing.T) {
	cases := []struct {
		name        string
		savePath    string
		torrentName string
		contentName string
		want        bool
	}{
		{
			name:        "basename 与种子名一致",
			savePath:    "/m/The.Italian.Job.2003.UHD.Blu-ray.2160p.HEVC-DTS-HD.MA5.1-DiY@HDHome",
			torrentName: "The.Italian.Job.2003.UHD.Blu-ray.2160p.HEVC-DTS-HD.MA5.1-DiY@HDHome",
			want:        true,
		},
		{
			name:        "空格命名与点号命名可互认",
			savePath:    "/downloads/The Solitary Gourmet S04 SP 2014 1080p",
			torrentName: "The.Solitary.Gourmet.S04.SP.2014.1080p",
			want:        true,
		},
		{
			name:        "挂载根与种子名无关",
			savePath:    "/m",
			torrentName: "The.Solitary.Gourmet.S04.SP.2014.BluRay.iPad.1080p.AAC.x265-10bit-HDSPad",
			contentName: "The Solitary Gourmet SP S04 2014 1080p iPad BluRay x265 AAC-10bit-HDSPad",
			want:        false,
		},
		{
			name:        "分类根与种子名无关",
			savePath:    "/m/BluRay",
			torrentName: "Some.Movie.2024.2160p.WEB-DL",
			want:        false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := savePathLooksLikeReleaseDir(tc.savePath, tc.torrentName, tc.contentName); got != tc.want {
				t.Fatalf("savePathLooksLikeReleaseDir(%q) = %t, want %t", tc.savePath, got, tc.want)
			}
		})
	}
}

// TestBuildMediaPathCandidatesRestrictsSavePathRecursion 验证候选生成时的递归授权范围：
// 种子名/内容名派生的候选始终可递归；裸 save_path 只有在它本身就是发布目录时才可递归，
// 其余情况仍允许在顶层挑文件（保住"下载器返回无关末级目录名但片子在里面"的既有场景）。
func TestBuildMediaPathCandidatesRestrictsSavePathRecursion(t *testing.T) {
	torrentName := "The.Solitary.Gourmet.S04.SP.2014.BluRay.iPad.1080p.AAC.x265-10bit-HDSPad"
	contentName := "The Solitary Gourmet SP S04 2014 1080p iPad BluRay x265 AAC-10bit-HDSPad"

	mountRoot := buildMediaPathCandidates([]string{"/m"}, torrentName, contentName)
	if len(mountRoot) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(mountRoot))
	}
	if !mountRoot[0].Recursive || !mountRoot[1].Recursive {
		t.Fatalf("torrent_name/content_name 候选应始终允许递归: %#v", mountRoot[:2])
	}
	last := mountRoot[2]
	if !last.AllowDirScan {
		t.Fatalf("裸 save_path 仍应允许顶层挑文件（保持既有语义）")
	}
	if last.Recursive {
		t.Fatalf("挂载根形态的 save_path 不应允许递归扫描: %#v", last)
	}

	releaseDir := filepath.Join("/m", torrentName)
	matched := buildMediaPathCandidates([]string{releaseDir}, torrentName, contentName)
	if !matched[len(matched)-1].Recursive {
		t.Fatalf("save_path 本身就是发布目录时应允许递归: %#v", matched[len(matched)-1])
	}
}

// TestFindMediaSiblingByBaseNameRejectsRealMediaExtension 验证"已带扩展名"的判定只看媒体扩展名：
// 真实发布名（点分隔、以 -GRP 收尾）必须继续走同名回退，只有传入 .mkv/.mp4 等媒体文件本身才拒绝。
func TestFindMediaSiblingByBaseNameRejectsRealMediaExtension(t *testing.T) {
	tempDir := t.TempDir()
	if _, err := findMediaSiblingByBaseName(filepath.Join(tempDir, "Movie.2021.1080p.x264-GRP.mkv"), true); err == nil {
		t.Fatalf("已带媒体扩展名的路径应被拒绝")
	}
}

// TestPickMediaEntryNonRecursiveIgnoresSiblingReleases 复现故障的最小场景：
// 挂载根下同时存在本种子的普通文件和邻居的蓝光原盘分段，且邻居分段更大。
// 递归扫描会挑中邻居（旧行为），非递归必须只在本目录顶层找、从而拒绝越界。
func TestPickMediaEntryNonRecursiveIgnoresSiblingReleases(t *testing.T) {
	root := t.TempDir()
	ownDirName := "The.Solitary.Gourmet.S04.SP.2014.BluRay.iPad.1080p.AAC.x265-10bit-HDSPad"
	siblingDiscName := "The.Italian.Job.2003.UHD.Blu-ray.2160p.HEVC.DTS-HD.MA5.1-DiY@HDHome"
	writeMediaTestFile(t, filepath.Join(root, ownDirName, ownDirName+".mp4"), 1<<20)
	writeMediaTestFile(t, filepath.Join(root, siblingDiscName, "BDMV", "STREAM", "00100.m2ts"), 8<<20)

	if _, err := pickMediaEntry(root, true, false); err == nil {
		t.Fatalf("非递归扫描不应在只含子目录的挂载根上找到媒体文件")
	}

	// 对照：旧的递归语义确实会挑到邻居更大的分段，这正是必须收窄的原因。
	picked, err := pickMediaEntry(root, true, true)
	if err != nil {
		t.Fatalf("recursive pick unexpectedly failed: %v", err)
	}
	if !strings.Contains(picked, siblingDiscName) {
		t.Fatalf("递归扫描应命中邻居分段（用于对照），got %s", picked)
	}
}

// TestDetectBlurayDiscByRootsRejectsSiblingDiscUnderMountRoot 端到端复现：
// 种子是《舌尖》普通 mp4，save_path 是挂载根，邻居有一部 4K 原盘。
// 修复前会被判为"本种子是蓝光原盘"并把邻居路径写入结果；修复后必须判为非原盘。
func TestDetectBlurayDiscByRootsRejectsSiblingDiscUnderMountRoot(t *testing.T) {
	root := t.TempDir()
	ownDirName := "The.Solitary.Gourmet.S04.SP.2014.BluRay.iPad.1080p.AAC.x265-10bit-HDSPad"
	siblingDiscName := "The.Italian.Job.2003.UHD.Blu-ray.2160p.HEVC.DTS-HD.MA5.1-DiY@HDHome"
	writeMediaTestFile(t, filepath.Join(root, ownDirName, ownDirName+".mp4"), 1<<20)
	siblingDisc := filepath.Join(root, siblingDiscName)
	writeMediaTestFile(t, filepath.Join(siblingDisc, "BDMV", "STREAM", "00100.m2ts"), 8<<20)
	writeMediaTestFile(t, filepath.Join(siblingDisc, "CERTIFICATE", "APPINFO.bd"), 1<<10)

	isBluray, detected := DetectBlurayDiscByRoots([]string{root}, ownDirName, "")
	if isBluray {
		t.Fatalf("挂载根下的邻居原盘不应被判为本种子的原盘，got %s", detected)
	}

	// 本种子目录自己就是原盘时必须仍然命中，防止收窄规则误杀真实原盘。
	ownDiscName := "The.Fire.Raven.2025.2160p.UHD.Blu-ray.HEVC.DTS-HD.MA5.1-HHWEB"
	ownDisc := filepath.Join(root, ownDiscName)
	writeMediaTestFile(t, filepath.Join(ownDisc, "BDMV", "STREAM", "00000.m2ts"), 4<<20)
	writeMediaTestFile(t, filepath.Join(ownDisc, "CERTIFICATE", "APPINFO.bd"), 1<<10)

	isBluray, detected = DetectBlurayDiscByRoots([]string{root}, ownDiscName, "")
	if !isBluray {
		t.Fatalf("本种子目录自身是原盘时应命中，got false (detected=%s)", detected)
	}
	if !strings.Contains(detected, ownDiscName) {
		t.Fatalf("命中的原盘应是本种子目录，got %s", detected)
	}
}
