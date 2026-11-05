package repository

import "strings"

// groupNameTokenSeparators 是种子名（torrents.name）里分隔组名 token 的字符。
// 覆盖 PT 发布名常见写法：点号（.FRDS）、横杠（-MNHD-FRDS / -BMDru）、
// 空格、下划线、方括号、@ 上传者标记。
const groupNameTokenSeparators = ".-_ []()@"

// ungroupedNameTechTokens 是「未拉取种子名字兜底」专用的技术 token 黑名单。
// 独立于其他包的定义，避免把 1080p / x265 / DDP / 2024 之类当组名。
var ungroupedNameTechTokens = map[string]struct{}{
	"x264": {}, "x265": {}, "h264": {}, "h265": {}, "hevc": {}, "av1": {}, "avc": {}, "vp9": {}, "vp8": {}, "mpeg2": {}, "xvid": {}, "divx": {},
	"480p": {}, "576p": {}, "720p": {}, "1080p": {}, "1440p": {}, "2160p": {}, "4320p": {}, "4k": {}, "8k": {}, "uhd": {}, "fhd": {}, "hd": {}, "sd": {},
	"hdr": {}, "hdr10": {}, "hdr10plus": {}, "sdr": {}, "dolbyvision": {}, "dovi": {}, "dv": {}, "10bit": {}, "8bit": {}, "12bit": {},
	"dd": {}, "ddp": {}, "aac": {}, "ac3": {}, "eac3": {}, "dts": {}, "dtsx": {}, "dtshd": {}, "ma": {}, "hr": {}, "truehd": {}, "atmos": {},
	"pcm": {}, "flac": {}, "opus": {}, "ogg": {}, "ape": {}, "alac": {}, "wav": {}, "mp3": {}, "mp2": {}, "lpcm": {}, "dsd": {},
	"web": {}, "webdl": {}, "webrip": {}, "hdtv": {}, "sdtv": {}, "uhdtv": {}, "bluray": {}, "bdmux": {}, "bdrip": {}, "brrip": {}, "dvdrip": {}, "dvd": {}, "remux": {}, "hdrip": {}, "dl": {}, "rip": {}, "audio": {}, "audios": {},
	"complete": {}, "multi": {}, "dual": {}, "chs": {}, "cht": {}, "chi": {}, "eng": {}, "jpn": {}, "zh": {}, "uncut": {}, "extended": {}, "imax": {}, "3d": {}, "ai": {}, "pack": {},
	"collection": {}, "mixed": {}, "movies": {}, "movie": {}, "top250": {}, "s01": {}, "e01": {},
	// 通用英文词（合集/种子描述里常见，不是发布组名）
	"seed": {}, "component": {}, "release": {}, "the": {}, "and": {}, "of": {}, "part": {}, "vol": {},
	"internal": {}, "external": {}, "group": {}, "nogroup": {}, "aka": {}, "nostr": {},
}

// parseGroupNameTokensFromTorrentName 从未拉取种子的种子名里切出候选组名 token。
//
// 这是「未拉取种子兜底」专用的新方法，独立于标题解析组件（BuildSimpleTitleComponents）：
//   - 不参与任何现有解析/持久化链路，仅由 QueryUngroupedStats 在没有
//     seed_parameters.title_components 时调用；
//   - 现成标题解析器对 "...Collection.20240501.FRDS" 这类点号合集名解析不出制作组，
//     且 "WEB-DL-FRDS" 会产出 "DL-FRDS" 这种脏值，故此处按分隔符切 token 后
//     交由 matchKnownGroupSite 与官组清单比对，口径与官组统计一致。
//
// 参数/返回：name 为 torrents.name；返回去重后的候选 token（保持出现顺序，忽略大小写）。
// 失败场景：无。
// 副作用：无。
func parseGroupNameTokensFromTorrentName(name string) []string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil
	}
	raw := strings.FieldsFunc(trimmed, func(r rune) bool {
		return strings.ContainsRune(groupNameTokenSeparators, r)
	})
	seen := map[string]struct{}{}
	tokens := make([]string, 0, len(raw))
	for _, part := range raw {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		lower := strings.ToLower(token)
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

// resolveGroupNameFromTorrentName 从未拉取种子的种子名中解析组名并判定归属：
//   - 命中站点官组清单（精确 + 长度≥3 部分匹配）→ 返回 (归属站点, 组名文本, true)
//   - 命中清单外 token → 返回 ("", 外部组名, false)
//   - 没有任何可用 token → 返回 ("", "", false)
//
// 参数/返回：name 为种子名；matchSite 为复用 QueryUngroupedStats 的官组清单匹配函数。
// 失败场景：无。
// 副作用：无。
func resolveGroupNameFromTorrentName(
	name string,
	matchSite func(team string) string,
) (site string, group string, known bool) {
	tokens := parseGroupNameTokensFromTorrentName(name)
	if len(tokens) == 0 {
		return "", "", false
	}
	// 只考察名字尾部若干 token：发布组名几乎总在末尾，避免把片名里的普通词
	// （如 No.Component.Seed 的 Component/Seed）误当成外部组名。
	const maxTailTokens = 4
	start := len(tokens) - maxTailTokens
	if start < 0 {
		start = 0
	}
	tail := tokens[start:]
	// 优先选命中官组清单的 token（从后往前）。
	for i := len(tail) - 1; i >= 0; i-- {
		if s := matchSite(tail[i]); s != "" {
			return s, tail[i], true
		}
	}
	// 没有命中官组：取尾部像组名的 token 作为外部组名。
	for i := len(tail) - 1; i >= 0; i-- {
		if isLikelyGroupNameToken(tail[i]) {
			return "", tail[i], false
		}
	}
	return "", "", false
}

// isLikelyGroupNameToken 粗判 token 是否像发布组名（避免把 1080p/x265/2024 之类
// 技术串当成外部组名）。仅用于「未拉取种子」兜底，不影响其他逻辑。
func isLikelyGroupNameToken(token string) bool {
	// 长度≥3：No/3D/A.I 这类 1~2 字符普通词/缩写不作为外部组名，
	// BMDru/FLTBK/WiKi/FRDS 等真实发布组名不受影响。
	if len(token) < 3 || len(token) > 24 {
		return false
	}
	lower := strings.ToLower(token)
	// 纯数字（年份/集数/序号）、分辨率/位深/编码/音频等技术串
	if strings.Trim(lower, "0123456789") == "" {
		return false
	}
	if _, hit := ungroupedNameTechTokens[lower]; hit {
		return false
	}
	// 含点号的复合技术串（DDP5.1 / H.264）在切分时已按点号断开，此处兜底
	letters := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, lower)
	if letters != "" {
		if _, hit := ungroupedNameTechTokens[letters]; hit {
			return false
		}
	}
	return true
}
