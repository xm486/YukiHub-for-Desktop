package service

import (
	"strings"
	"testing"

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
