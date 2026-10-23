package media

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
)

const blurayDetectLogModule = "蓝光判定"

// DetectBlurayDiscByCandidates 按候选路径探测是否为蓝光原盘目录。
// 参数/返回：savePath 为下载目录，torrentName/contentName 为候选子目录名；返回是否命中及最终命中的路径。
// 失败场景：savePath 为空时返回 false 与空路径。
// 副作用：读取文件系统并输出蓝光判定日志。
func DetectBlurayDiscByCandidates(savePath, torrentName, contentName string) (bool, string) {
	return DetectBlurayDiscByRoots([]string{savePath}, torrentName, contentName)
}

// DetectBlurayDiscByRoots 按多个保存路径根探测是否为蓝光原盘目录。
// 参数/返回：savePaths 为按优先级排列的保存路径根；torrentName/contentName 为候选子路径；返回是否命中及最终命中的路径。
// 失败场景：savePaths 全为空时返回 false 与空路径。
// 副作用：读取文件系统并输出蓝光判定日志。
func DetectBlurayDiscByRoots(savePaths []string, torrentName, contentName string) (bool, string) {
	candidates := buildMediaPathCandidates(savePaths, torrentName, contentName)
	fallbackPath := firstNonEmptyMediaRoot(savePaths)
	if len(candidates) == 0 {
		logx.Warnf(blurayDetectLogModule, "参数异常：save_paths 为空")
		return false, fallbackPath
	}
	logx.Infof(
		blurayDetectLogModule,
		"候选路径生成：save_paths=%v torrent_name=%s content_name=%s candidates=%s",
		savePaths, strings.TrimSpace(torrentName), strings.TrimSpace(contentName), formatMediaPathCandidatesForLog(candidates),
	)

	for _, candidate := range candidates {
		logx.Infof(
			blurayDetectLogModule,
			"检测候选路径：source=%s candidate=%s allow_dir_scan=%t",
			normalizeMediaCandidateSource(candidate.Source), candidate.Path, candidate.AllowDirScan,
		)
		access, err := resolveMediaAccessForCandidate(candidate, "蓝光判定")
		if err != nil {
			logx.Warnf(blurayDetectLogModule, "解析候选路径失败：source=%s candidate=%s err=%v", normalizeMediaCandidateSource(candidate.Source), candidate.Path, err)
			continue
		}
		root := FindBlurayRootPath(access.ResolvedPath)
		closeErr := access.Close()
		if closeErr != nil {
			logx.Warnf(blurayDetectLogModule, "关闭候选路径访问会话失败：candidate=%s err=%v", candidate.Path, closeErr)
		}
		if root != "" && !blurayRootOwnedByCandidate(candidate, access, root) {
			logx.Warnf(
				blurayDetectLogModule,
				"原盘根不属于该候选，判为不命中：source=%s candidate=%s root=%s",
				normalizeMediaCandidateSource(candidate.Source), candidate.Path, root,
			)
			root = ""
		}
		if root != "" {
			logx.Warnf(blurayDetectLogModule, "命中原盘目录：candidate=%s source=%s root=%s", candidate.Path, access.SourcePath, root)
			if strings.TrimSpace(access.SourcePath) != "" {
				return true, access.SourcePath
			}
			return true, candidate.Path
		}
	}
	logx.Infof(blurayDetectLogModule, "未命中原盘目录：fallback_path=%s", fallbackPath)
	return false, fallbackPath
}

// blurayRootOwnedByCandidate 判断回溯到的蓝光原盘根能否归属于当前候选。
// 归属未经确认的目录（Recursive=false，即 save_path 很可能是挂载根/分类根）只承认"该目录本身就是原盘根"，
// 不允许通过递归在同根下发现别的发布目录再当成本种子的原盘：邻居的 4K 原盘体积通常远大于本种子的文件，
// 一旦被递归命中就会把别人的 BDInfo 写进本种子（历史故障：《舌尖》拿到了《The Italian Job》的信息）。
// 参数/返回：允许归属时返回 true；候选是文件、或来自 ISO 挂载时同样放行。
func blurayRootOwnedByCandidate(candidate mediaPathCandidate, access *LocalMediaAccess, root string) bool {
	if candidate.Recursive {
		return true
	}
	if access != nil && access.Session != nil && access.Session.Mounted {
		return true
	}
	cleanCandidate := filepath.Clean(strings.TrimSpace(candidate.Path))
	cleanRoot := filepath.Clean(strings.TrimSpace(root))
	if cleanCandidate == "" || cleanRoot == "" {
		return false
	}
	if info, err := os.Stat(cleanCandidate); err == nil && !info.IsDir() {
		return true
	}
	return cleanRoot == cleanCandidate
}

// FindBlurayRootPath 从给定路径向上回溯蓝光根目录。
// 参数/返回：rawPath 可为文件或目录；命中时返回包含 BDMV/CERTIFICATE 的根目录。
// 失败场景：路径不存在或不满足蓝光目录结构时返回空字符串。
// 副作用：访问文件系统。
func FindBlurayRootPath(rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return ""
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return ""
	}

	current := trimmed
	if !info.IsDir() {
		current = filepath.Dir(trimmed)
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return ""
	}

	for depth := 0; depth < 10; depth++ {
		if isDir(filepath.Join(current, "BDMV")) && isDir(filepath.Join(current, "CERTIFICATE")) {
			return current
		}
		if strings.EqualFold(filepath.Base(current), "BDMV") {
			parent := filepath.Dir(current)
			if isDir(filepath.Join(parent, "CERTIFICATE")) {
				return parent
			}
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}
	return ""
}

func detectBlurayDiscAtPath(rawPath string) bool {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		logx.Debugf(blurayDetectLogModule, "跳过空路径")
		return false
	}
	bdmvPath := filepath.Join(trimmed, "BDMV")
	info, err := os.Stat(bdmvPath)
	if err != nil || !info.IsDir() {
		bdmvPath = filepath.Join(trimmed, "bdmv")
		info, err = os.Stat(bdmvPath)
		if err != nil || !info.IsDir() {
			logx.Debugf(blurayDetectLogModule, "未找到BDMV目录：candidate=%s", trimmed)
			return false
		}
	}
	logx.Infof(blurayDetectLogModule, "发现BDMV目录：candidate=%s bdmv_path=%s", trimmed, bdmvPath)

	candidates := []string{
		"index.bdmv", "INDEX.BDMV",
		"index.bdm", "INDEX.BDM",
		"MovieObject.bdmv", "MOVIEOBJECT.BDMV",
	}
	for _, fileName := range candidates {
		target := filepath.Join(bdmvPath, fileName)
		if _, err := os.Stat(target); err == nil {
			logx.Warnf(blurayDetectLogModule, "命中蓝光标记文件：candidate=%s marker=%s", trimmed, target)
			return true
		}
	}
	logx.Infof(blurayDetectLogModule, "缺少蓝光标记文件：candidate=%s bdmv_path=%s", trimmed, bdmvPath)

	return false
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func firstNonEmptyMediaRoot(paths []string) string {
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
