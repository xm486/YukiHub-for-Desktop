package service

import (
	"strings"
	"testing"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/version"
)

// devReleaseBody 取自真实发布的发布说明（`.github/workflows/autobuild.yml` 生成），
// 特意保留表格里的 `*-windows-*-portable.zip` —— 它含 `-windows-`，但前面没有
// `yukihub-`，不能被当成版本号。
func devReleaseBody(version string) string {
	return strings.Join([]string{
		"## 🚀 YukiHub Development Build",
		"",
		"**⚠️ 这是一个自动构建的开发版本**",
		"",
		"### 📋 版本信息",
		"",
		"- **版本号**: `" + version + "`",
		"- **基于版本**: `0.2.5`",
		"- **提交**: [`abc1234`](https://github.com/xm486/YukiHub-for-Desktop/commit/abc1234)",
		"",
		"### 📦 下载说明",
		"",
		"| 文件 | 说明 |",
		"|------|------|",
		"| `*-windows-*-portable.zip` | Windows 便携版，解压即用 |",
		"",
		"> 本次构建平台：Windows / Linux",
	}, "\n")
}

func devReleaseFixture() releaseResponse {
	return releaseResponse{
		TagName:     devReleaseTag,
		Name:        "开发版 - 0.2.5-dev.130+abc1234",
		Body:        devReleaseBody("0.2.5-dev.130+abc1234"),
		HTMLURL:     "https://github.com/xm486/YukiHub-for-Desktop/releases/tag/dev-latest",
		PublishedAt: "2026-10-11T09:30:00Z",
		Prerelease:  true,
		Assets: []releaseAsset{
			{
				Name: "YukiHub-0.2.5-dev.130+abc1234-windows-amd64-portable.zip",
				URL:  "https://example.invalid/portable.zip",
			},
			{
				Name: "YukiHub-0.2.5-dev.130+abc1234-linux-amd64.deb",
				URL:  "https://example.invalid/yukihub.deb",
			},
		},
	}
}

func TestExtractDevReleaseVersionFromBody(t *testing.T) {
	release := devReleaseFixture()

	got, err := extractDevReleaseVersion(release)
	if err != nil {
		t.Fatalf("extractDevReleaseVersion 失败: %v", err)
	}
	if got != "0.2.5-dev.130+abc1234" {
		t.Fatalf("版本号应为 0.2.5-dev.130+abc1234，实际 %q", got)
	}
}

func TestExtractDevReleaseVersionFromNameOnly(t *testing.T) {
	release := devReleaseFixture()
	release.Body = "## 空说明（没有版本号行）"
	release.Assets = nil

	got, err := extractDevReleaseVersion(release)
	if err != nil {
		t.Fatalf("extractDevReleaseVersion 失败: %v", err)
	}
	if got != "0.2.5-dev.130+abc1234" {
		t.Fatalf("应退回发布标题取版本号，实际 %q", got)
	}
}

func TestExtractDevReleaseVersionFromAssetName(t *testing.T) {
	release := devReleaseFixture()
	release.Body = ""
	release.Name = "开发版"

	got, err := extractDevReleaseVersion(release)
	if err != nil {
		t.Fatalf("extractDevReleaseVersion 失败: %v", err)
	}
	if got != "0.2.5-dev.130+abc1234" {
		t.Fatalf("应退回附件名取版本号，实际 %q", got)
	}
}

func TestExtractDevReleaseVersionFromMarker(t *testing.T) {
	release := devReleaseFixture()
	release.Body = "<!-- yukihub-version: 0.3.0-dev.7+deadbee -->\n正文里没有版本号行"

	got, err := extractDevReleaseVersion(release)
	if err != nil {
		t.Fatalf("extractDevReleaseVersion 失败: %v", err)
	}
	if got != "0.3.0-dev.7+deadbee" {
		t.Fatalf("机器可读标记优先，实际 %q", got)
	}
}

func TestExtractDevReleaseVersionRejectsUnparsable(t *testing.T) {
	release := devReleaseFixture()
	release.Body = "## 只有标题\n没有任何版本信息"
	release.Name = "开发版"
	release.Assets = []releaseAsset{{Name: "readme.txt", URL: "https://example.invalid/readme"}}

	if _, err := extractDevReleaseVersion(release); err == nil {
		t.Fatal("取不到版本号时必须报错，否则会拿空版本号去比较")
	}
}

func TestPickDevRelease(t *testing.T) {
	stable := releaseResponse{TagName: "v0.2.5"}
	dev := devReleaseFixture()
	draftDev := devReleaseFixture()
	draftDev.Draft = true

	tests := []struct {
		name     string
		releases []releaseResponse
		want     bool
	}{
		{name: "空列表", releases: nil, want: false},
		{name: "只有正式版", releases: []releaseResponse{stable}, want: false},
		{name: "草稿开发版不算", releases: []releaseResponse{draftDev}, want: false},
		{name: "正式版在前也能挑到", releases: []releaseResponse{stable, dev}, want: true},
		{name: "大小写不敏感", releases: []releaseResponse{{TagName: "DEV-LATEST"}}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, found := pickDevRelease(test.releases)
			if found != test.want {
				t.Fatalf("pickDevRelease = %v，期望 %v", found, test.want)
			}
		})
	}
}

func TestBuildDevUpdateInfoNeverOffersManifest(t *testing.T) {
	release := devReleaseFixture()
	// 就算发布里塞了清单附件（正式版才会有的东西），开发版也不提供应用内更新。
	release.Assets = append(release.Assets, releaseAsset{
		Name: "YukiHub-0.2.5-dev.130+abc1234-update-manifest.json",
		URL:  "https://example.invalid/manifest.json",
	})

	info, err := buildDevUpdateInfo(UpdateSourceGitHub, "xm486/YukiHub-for-Desktop", release)
	if err != nil {
		t.Fatalf("buildDevUpdateInfo 失败: %v", err)
	}
	if info.UpdateManifestURL != "" {
		t.Fatalf("开发版不应提供更新清单地址，实际 %q", info.UpdateManifestURL)
	}
	if info.Version != "0.2.5-dev.130+abc1234" {
		t.Fatalf("版本号错误：%q", info.Version)
	}
	if info.ReleaseDate != "2026-10-11" {
		t.Fatalf("发布日期错误：%q", info.ReleaseDate)
	}
	if info.Downloads["windows"] != "https://example.invalid/portable.zip" {
		t.Fatalf("Windows 下载入口错误：%q", info.Downloads["windows"])
	}
	if info.Downloads["release_page"] != info.ReleaseURL {
		t.Fatalf("release_page 应与 ReleaseURL 一致：%q / %q", info.Downloads["release_page"], info.ReleaseURL)
	}
}

func TestReleaseAPIURLsUseExpectedEndpoints(t *testing.T) {
	slug := "xm486/YukiHub-for-Desktop"

	if got := releaseAPIURL(UpdateSourceGitHub, slug); got != "https://api.github.com/repos/"+slug+"/releases/latest" {
		t.Fatalf("正式版 GitHub 地址错误：%s", got)
	}
	if got := releaseListAPIURL(UpdateSourceGitHub, slug); !strings.Contains(got, "/releases?") {
		t.Fatalf("开发版必须走发布列表接口（releases/latest 不含预发布），实际 %s", got)
	}
	if got := releaseListAPIURL(UpdateSourceGitCode, slug); got != "https://gitcode.com/api/v5/repos/"+slug+"/releases" {
		t.Fatalf("开发版 GitCode 地址错误：%s", got)
	}
}

func TestUpdateChannelForConfig(t *testing.T) {
	tests := []struct {
		name      string
		appConfig *appconf.AppConfig
		want      string
	}{
		{name: "nil 配置按正式版", appConfig: nil, want: UpdateChannelStable},
		{name: "默认关闭开发版", appConfig: &appconf.AppConfig{}, want: UpdateChannelStable},
		{name: "勾选后走开发版", appConfig: &appconf.AppConfig{CheckDevUpdates: true}, want: UpdateChannelDev},
		{
			name:      "自定义更新源固定正式版",
			appConfig: &appconf.AppConfig{CheckDevUpdates: true, UpdateCheckURL: "https://example.invalid/version.json"},
			want:      UpdateChannelStable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := updateChannelForConfig(test.appConfig); got != test.want {
				t.Fatalf("updateChannelForConfig = %q，期望 %q", got, test.want)
			}
		})
	}
}

// TestHasDevUpdatePrefersTimeComparison 锁住开发版通道的判断规则：
// 提交号相同 → 同一个构建；都有时间 → 比时间；否则退回版本号。
//
// 为什么不能只比版本号：开发版的版本是 `<基础版本>-dev.<提交数>+<短提交>`，
// 首个 tag 之前基础版本恒为 0.0.0，提交数在 rebase / squash 后还会回退。
func TestHasDevUpdatePrefersTimeComparison(t *testing.T) {
	tests := []struct {
		name    string
		local   localBuildInfo
		release *UpdateInfo
		want    bool
	}{
		{
			name: "提交号相同 → 同一个构建，发布时间晚几秒也不算更新",
			local: localBuildInfo{
				version:   "0.0.0-dev.168+2ac48ae",
				commit:    "2ac48aea657e4b851a72ca9473af483b7be98008", // 注入的是完整号
				buildTime: "2026-10-09T13:52:17Z",
			},
			release: &UpdateInfo{
				Version:          "0.0.0-dev.168+2ac48ae",
				BuildCommit:      "2ac48ae", // 版本号里是短号
				ReleaseTimestamp: "2026-10-09T13:52:21Z",
			},
			want: false,
		},
		{
			name: "同一个构建重新发布（同提交、发布时间晚半小时）→ 仍然不提示",
			// 手动重跑一次开发版构建会覆盖 dev-latest：提交号没变、版本号一样，
			// 只有发布时间变新。没有这一条（只靠时间），用户会为了一个和手上一模
			// 一样的包被反复提示更新。
			local: localBuildInfo{
				version:   "0.0.0-dev.168+2ac48ae",
				commit:    "2ac48ae",
				buildTime: "2026-10-09T13:52:17Z",
			},
			release: &UpdateInfo{
				Version:          "0.0.0-dev.168+2ac48ae",
				BuildCommit:      "2ac48ae",
				ReleaseTimestamp: "2026-10-09T14:25:00Z",
			},
			want: false,
		},
		{
			name: "发布比本机构建晚 18 分钟 → 有更新",
			local: localBuildInfo{
				version:   "0.0.0-dev.168+2ac48ae",
				commit:    "2ac48ae",
				buildTime: "2026-10-09T13:52:17Z",
			},
			release: &UpdateInfo{
				Version:          "0.0.0-dev.170+abc1234",
				BuildCommit:      "abc1234",
				ReleaseTimestamp: "2026-10-09T14:10:00Z",
			},
			want: true,
		},
		{
			name: "只晚 30 秒：多半是同一个构建的发布延迟，不提示",
			local: localBuildInfo{
				version:   "0.0.0-dev.168+2ac48ae",
				commit:    "2ac48ae",
				buildTime: "2026-10-09T13:52:17Z",
			},
			release: &UpdateInfo{
				Version:          "0.0.0-dev.169+abc1234",
				BuildCommit:      "abc1234",
				ReleaseTimestamp: "2026-10-09T13:52:47Z",
			},
			want: false,
		},
		{
			name: "发布比本机旧（用户自己编了更新的构建）→ 不劝人更新",
			local: localBuildInfo{
				version:   "0.0.0-dev.200+fff0000",
				commit:    "fff0000",
				buildTime: "2026-10-11T02:00:00Z",
			},
			release: &UpdateInfo{
				Version:          "0.0.0-dev.168+2ac48ae",
				BuildCommit:      "2ac48ae",
				ReleaseTimestamp: "2026-10-09T13:52:21Z",
			},
			want: false,
		},
		{
			name: "两边时间都拿不到 → 退回版本号比较（开发版更新）",
			local: localBuildInfo{
				version:   "0.0.0-dev.1+aaa1111",
				commit:    "unknown",
				buildTime: "unknown",
			},
			release: &UpdateInfo{Version: "0.0.0-dev.168+2ac48ae"},
			want:    true,
		},
		{
			name: "两边时间都拿不到 + 版本相同 → 无更新",
			local: localBuildInfo{
				version:   "0.0.0-dev.168+2ac48ae",
				commit:    "unknown",
				buildTime: "unknown",
			},
			release: &UpdateInfo{Version: "0.0.0-dev.168+2ac48ae"},
			want:    false,
		},
		{
			name: "本机是正式版、时间不可比 → 退回版本号（同核心 dev 优先于正式版）",
			local: localBuildInfo{
				version:   "0.2.5",
				commit:    "unknown",
				buildTime: "unknown",
			},
			release: &UpdateInfo{Version: "0.2.5-dev.130+abc1234"},
			want:    true,
		},
		{
			name: "平台没给时间但本机有 → 退回版本号",
			local: localBuildInfo{
				version:   "0.1.0",
				commit:    "unknown",
				buildTime: "2026-10-01T00:00:00Z",
			},
			release: &UpdateInfo{Version: "0.2.0"},
			want:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := hasDevUpdate(test.local, test.release)
			if err != nil {
				t.Fatalf("hasDevUpdate 报错: %v", err)
			}
			if got != test.want {
				t.Fatalf("hasDevUpdate = %v，期望 %v", got, test.want)
			}
		})
	}
}

func TestSameBuildCommit(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "短号 vs 完整号", left: "2ac48ae", right: "2ac48aea657e4b851a72ca9473af483b7be98008", want: true},
		{name: "大小写不同", left: "2AC48AE", right: "2ac48ae", want: true},
		{name: "不同提交", left: "2ac48ae", right: "abc1234", want: false},
		{name: "一侧 unknown", left: "unknown", right: "2ac48ae", want: false},
		{name: "一侧为空", left: "", right: "2ac48ae", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameBuildCommit(test.left, test.right); got != test.want {
				t.Fatalf("sameBuildCommit(%q, %q) = %v，期望 %v", test.left, test.right, got, test.want)
			}
		})
	}
}

func TestParseBuildTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantOK  bool
		wantUTC string
	}{
		{name: "RFC3339 UTC", value: "2026-10-09T13:52:21Z", wantOK: true, wantUTC: "2026-10-09T13:52:21Z"},
		{name: "老构建的本地时间裸字符串", value: "2026-10-11 19:25:06", wantOK: true},
		{name: "unknown", value: "unknown", wantOK: false},
		{name: "空串", value: "  ", wantOK: false},
		{name: "乱七八糟", value: "yesterday", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, ok := parseBuildTimestamp(test.value)
			if ok != test.wantOK {
				t.Fatalf("parseBuildTimestamp(%q) ok = %v，期望 %v", test.value, ok, test.wantOK)
			}
			if ok && test.wantUTC != "" {
				if got := parsed.UTC().Format(time.RFC3339); got != test.wantUTC {
					t.Fatalf("解析结果 = %s，期望 %s", got, test.wantUTC)
				}
			}
		})
	}
}

func TestDevBuildCommit(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		{version: "0.2.5-dev.130+abc1234", want: "abc1234"},
		{version: "0.2.5-dev.130+ABC1234", want: "ABC1234"},
		{version: "0.2.5-dev.130", want: ""},
		{version: "0.2.5-dev.130+nope", want: ""},
		{version: "0.2.5", want: ""},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if got := devBuildCommit(test.version); got != test.want {
				t.Fatalf("devBuildCommit(%q) = %q，期望 %q", test.version, got, test.want)
			}
		})
	}
}

// TestBuildDevUpdateInfoCarriesCommitAndTimestamp 保证开发版更新信息带上
// 「提交号 + 完整发布时间」——没有它们就没法按时间判断谁更新。
func TestBuildDevUpdateInfoCarriesCommitAndTimestamp(t *testing.T) {
	release := devReleaseFixture()

	info, err := buildDevUpdateInfo(UpdateSourceGitHub, "xm486/YukiHub-for-Desktop", release)
	if err != nil {
		t.Fatalf("buildDevUpdateInfo 失败: %v", err)
	}
	if info.BuildCommit != "abc1234" {
		t.Fatalf("BuildCommit = %q，期望 abc1234", info.BuildCommit)
	}
	if info.ReleaseTimestamp != "2026-10-11T09:30:00Z" {
		t.Fatalf("ReleaseTimestamp = %q，期望发布原始时间戳", info.ReleaseTimestamp)
	}
}

// TestCompareVersionsAcrossDevChannel 锁住「开发版优先于同核心正式版」这条既有约定，
// 它同时决定了「正式版用户勾选开发版通道后能看到开发版更新」。
func TestCompareVersionsAcrossDevChannel(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "正式版 → 同核心开发版有更新", current: "0.2.5", latest: "0.2.5-dev.130+abc1234", want: true},
		{name: "开发版 → 更新的开发版有更新", current: "0.2.5-dev.128+aaa1111", latest: "0.2.5-dev.130+abc1234", want: true},
		{name: "同一个开发版无更新", current: "0.2.5-dev.130+abc1234", latest: "0.2.5-dev.130+abc1234", want: false},
		{name: "开发版 → 更高核心开发版有更新", current: "0.2.5-dev.130+abc1234", latest: "0.3.0-dev.1+bbb2222", want: true},
		{name: "高于对方时不提示", current: "0.3.0", latest: "0.2.5-dev.130+abc1234", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := compareVersions(test.current, test.latest)
			if err != nil {
				t.Fatalf("compareVersions 报错: %v", err)
			}
			if got != test.want {
				t.Fatalf("compareVersions(%q, %q) = %v，期望 %v", test.current, test.latest, got, test.want)
			}
		})
	}
}

// TestBuildDevUpdateInfoVersionMatchesInstalledBuild 用当前构建的真实版本号做一次
// 端到端比较：保证「拿开发版发布说明里的版本号」与 version.Version 是同一套格式。
func TestBuildDevUpdateInfoVersionMatchesInstalledBuild(t *testing.T) {
	release := devReleaseFixture()
	info, err := buildDevUpdateInfo(UpdateSourceGitHub, "xm486/YukiHub-for-Desktop", release)
	if err != nil {
		t.Fatalf("buildDevUpdateInfo 失败: %v", err)
	}

	if _, err := compareVersions(version.Version, info.Version); err != nil {
		t.Fatalf("当前版本 %q 与开发版版本号 %q 无法比较: %v", version.Version, info.Version, err)
	}
}
