package service

import (
	"fmt"
	"strconv"
	"strings"
)

// 版本号比较。
//
// YukiHub 的版本号**段数不固定**（与 Android 手机版保持一致）：历史上出现过
// 0.1、0.2、0.1.0、0.2.5、0.1.2.5、0.2.6.1 —— 因此不能套用严格的 SemVer 三段式
// （`semver.IsValid("0.1")` 为 false，会把合法版本判成非法）。
//
// 规则：
//   - 数字段逐段比较，段数不足的一方按 0 补齐（0.1 == 0.1.0；0.2 > 0.1.5）
//   - 构建元数据（+xxx）忽略
//   - 预发布标识（-dev.412 / -rc.1）沿用 SemVer 优先级：有预发布 < 无预发布
//   - 项目特例：同核心版本下 **dev 构建优先于正式版**（开发版比它要取代的版本新）
//   - 裸 `dev` 版本不提示更新

type parsedVersion struct {
	segments []int
	pre      string
	hasPre   bool
}

// compareVersions 比较两个版本号，返回 (true, nil) 表示 v1 < v2（即需要更新）。
func compareVersions(v1, v2 string) (bool, error) {
	if isBareDevVersion(v1) || isBareDevVersion(v2) {
		return false, nil // 裸 dev 版本不提示更新
	}

	parsedV1, err := parseVersion(v1)
	if err != nil {
		return false, err
	}
	parsedV2, err := parseVersion(v2)
	if err != nil {
		return false, err
	}

	return compareParsedVersions(parsedV1, parsedV2) < 0, nil
}

func isBareDevVersion(value string) bool {
	return strings.TrimPrefix(strings.TrimSpace(value), "v") == "dev"
}

// parseVersion 拆出版本号的数字段与预发布标识，段数不限。
func parseVersion(value string) (parsedVersion, error) {
	trimmed := strings.TrimSpace(value)
	withoutPrefix := strings.TrimPrefix(trimmed, "v")

	// 兼容旧 autobuild 受滚动标签 dev-latest 影响生成的非法版本号。
	// 由于这类版本缺失正式基础版本，将其视为 0.0.0 的开发预发布版本。
	if strings.HasPrefix(withoutPrefix, "dev-latest-dev.") {
		withoutPrefix = "0.0.0-" + strings.TrimPrefix(withoutPrefix, "dev-latest-")
	}

	if withoutPrefix == "" {
		return parsedVersion{}, fmt.Errorf("invalid version format: %s", trimmed)
	}

	// 构建元数据（+...）不参与比较
	if index := strings.IndexByte(withoutPrefix, '+'); index >= 0 {
		withoutPrefix = withoutPrefix[:index]
	}

	// 预发布标识（-...）
	var prerelease string
	hasPrerelease := false
	if index := strings.IndexByte(withoutPrefix, '-'); index >= 0 {
		prerelease = withoutPrefix[index+1:]
		hasPrerelease = true
		withoutPrefix = withoutPrefix[:index]
	}

	rawSegments := strings.Split(withoutPrefix, ".")
	segments := make([]int, 0, len(rawSegments))
	for _, raw := range rawSegments {
		segment, err := strconv.Atoi(raw)
		if err != nil || segment < 0 {
			return parsedVersion{}, fmt.Errorf("invalid version format: %s", trimmed)
		}
		segments = append(segments, segment)
	}

	return parsedVersion{segments: segments, pre: prerelease, hasPre: hasPrerelease}, nil
}

func compareParsedVersions(left, right parsedVersion) int {
	segmentCount := len(left.segments)
	if len(right.segments) > segmentCount {
		segmentCount = len(right.segments)
	}
	for index := 0; index < segmentCount; index++ {
		leftSegment := segmentAt(left.segments, index)
		rightSegment := segmentAt(right.segments, index)
		if leftSegment != rightSegment {
			if leftSegment < rightSegment {
				return -1
			}
			return 1
		}
	}

	// 数字段相同 —— 项目特例：dev 构建优先于同核心版本的正式版
	leftIsDev := isDevelopmentPrerelease(left.pre)
	rightIsDev := isDevelopmentPrerelease(right.pre)
	if leftIsDev && !right.hasPre {
		return 1
	}
	if !left.hasPre && rightIsDev {
		return -1
	}

	// 其余按 SemVer：有预发布的一方更小
	if left.hasPre != right.hasPre {
		if left.hasPre {
			return -1
		}
		return 1
	}
	if !left.hasPre {
		return 0
	}

	return comparePrerelease(left.pre, right.pre)
}

// comparePrerelease 按 SemVer 规则比较预发布标识（不含前导 '-'）。
func comparePrerelease(left, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	partCount := len(leftParts)
	if len(rightParts) > partCount {
		partCount = len(rightParts)
	}

	for index := 0; index < partCount; index++ {
		if index >= len(leftParts) {
			return -1 // 段数少的更小
		}
		if index >= len(rightParts) {
			return 1
		}

		leftNumber, leftIsNumber := numericIdentifier(leftParts[index])
		rightNumber, rightIsNumber := numericIdentifier(rightParts[index])
		switch {
		case leftIsNumber && rightIsNumber:
			if leftNumber != rightNumber {
				if leftNumber < rightNumber {
					return -1
				}
				return 1
			}
		case leftIsNumber:
			return -1 // 数字标识符 < 字母标识符
		case rightIsNumber:
			return 1
		default:
			if result := strings.Compare(leftParts[index], rightParts[index]); result != 0 {
				return result
			}
		}
	}

	return 0
}

func segmentAt(segments []int, index int) int {
	if index < len(segments) {
		return segments[index]
	}
	return 0
}

func isDevelopmentPrerelease(prerelease string) bool {
	return prerelease == "dev" || strings.HasPrefix(prerelease, "dev.")
}

func numericIdentifier(value string) (int, bool) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

// normalizeComparableVersion 把版本号规范成 `v<至少三段>[-预发布]` 形式，
// 段数不足三段时补 0（0.1 → v0.1.0）。用于拼装更新服务地址与跳过版本的比较。
func normalizeComparableVersion(value string) (string, error) {
	parsed, err := parseVersion(value)
	if err != nil {
		return "", err
	}

	segments := parsed.segments
	for len(segments) < 3 {
		segments = append(segments, 0)
	}

	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		parts = append(parts, strconv.Itoa(segment))
	}

	normalized := "v" + strings.Join(parts, ".")
	if parsed.hasPre {
		normalized += "-" + parsed.pre
	}

	return normalized, nil
}
