package service

import (
	"strings"
	"time"

	"yukihub/internal/version"
)

// 开发版通道怎么判断「有新构建」。
//
// 开发版**没有固定版本号**：CI 生成的版本形如 `<基础版本>-dev.<提交数>+<短提交>`，
// 打第一个 tag 之前基础版本恒为 `0.0.0`，提交数也只在历史线性时才递增
// （整段 rebase / squash 会让它回退）。所以这里以**构建时间**为准 ——
// 滚动构建的语义本来就是「谁更新看谁晚」：
//
//  1. 提交号相同 → 就是同一个构建，判「无更新」。这一条必须放在最前面：
//     CI 是先编译、几秒到几分钟后才发布，光比时间会把这个构建自己报成新版本；
//  2. 两边都有可比时间 → 发布时间晚于本机构建时间（且超出宽限期）才算有更新；
//     比本机旧则明确不提示（用户自己编了更新的构建时，不该劝他「更新」回旧构建）；
//  3. 时间拿不到（老构建没注入 BuildTimeUTC、平台也没给时间）→ 退回版本号比较，
//     也就是 version_compare.go 那一套（含「同核心版本 dev 优先于正式版」）。
const devUpdateGracePeriod = time.Minute

// localBuildInfo 是本机这个构建的身份，取自 internal/version 的 ldflags 注入值。
type localBuildInfo struct {
	version   string
	commit    string
	buildTime string
}

func currentBuildInfo() localBuildInfo {
	// 优先用 UTC 那份；老构建没注入它时退回本地时间裸字符串（按本机时区解释，
	// 只要本机的时区设置没变就仍然可用）。
	buildTime := version.BuildTimeUTC
	if _, ok := parseBuildTimestamp(buildTime); !ok {
		buildTime = version.BuildTime
	}
	return localBuildInfo{
		version:   version.Version,
		commit:    version.GitCommit,
		buildTime: buildTime,
	}
}

// sameBuildCommit 比较提交号：大小写不敏感，只比前 7 位 —— 版本号里带的是短号，
// 注入的 GitCommit 可能是完整号。任一为空或 `unknown` 时返回 false（无法断定，
// 交给时间判断）。
func sameBuildCommit(left string, right string) bool {
	normalizedLeft := normalizeBuildCommit(left)
	normalizedRight := normalizeBuildCommit(right)
	if normalizedLeft == "" || normalizedRight == "" {
		return false
	}
	return normalizedLeft == normalizedRight
}

const buildCommitShortLength = 7

func normalizeBuildCommit(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" || trimmed == "unknown" {
		return ""
	}
	if len(trimmed) > buildCommitShortLength {
		trimmed = trimmed[:buildCommitShortLength]
	}
	return trimmed
}

// hasDevUpdate 判断开发版发布是否比本机构建更新。
func hasDevUpdate(local localBuildInfo, release *UpdateInfo) (bool, error) {
	if release == nil {
		return false, nil
	}
	if sameBuildCommit(local.commit, release.BuildCommit) {
		return false, nil
	}

	releaseTime, releaseOK := parseBuildTimestamp(release.ReleaseTimestamp)
	localTime, localOK := parseBuildTimestamp(local.buildTime)
	if releaseOK && localOK {
		return releaseTime.After(localTime.Add(devUpdateGracePeriod)), nil
	}

	// 时间不可比（老构建 / 平台没给时间）→ 退回版本号比较
	return compareVersions(local.version, release.Version)
}

// parseBuildTimestamp 认两种时间表示：
//   - RFC3339（`2026-10-11T11:25:06Z`）：发布平台的 `published_at` / `created_at`，
//     以及构建脚本新注入的 `BuildTimeUTC`；
//   - `2006-01-02 15:04:05`：老构建注入的本地时间裸字符串，按本机时区解释。
func parseBuildTimestamp(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "unknown") {
		return time.Time{}, false
	}

	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed, true
	}
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", trimmed, time.Local); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}
