package appconf

import (
	"reflect"
	"testing"
	enums2 "yukihub/internal/common/enums"
)

func TestNormalizeMetadataSourcesAcceptsOptInSources(t *testing.T) {
	got := normalizeMetadataSources([]string{"bangumi", "bangumi_mirror", "nextmoe", "hikarinagi", "dlsite", "touchgal", "erogamescape", "BANGUMI_MIRROR", "unknown"})
	// bangumi_mirror 已下线：归一到 bangumi，并因去重只保留一份。
	want := []string{"bangumi", "nextmoe", "hikarinagi"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestNormalizeMetadataSourcesUsesExpectedDefaults(t *testing.T) {
	got := normalizeMetadataSources(nil)
	// 与手机版一致：默认启用五源，含 NextMoe。
	want := []string{"vndb", "bangumi", "ymgal", "hikarinagi", "nextmoe"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

// 已下线的 bangumi_mirror 必须静默降级成 bangumi，而不是被丢掉或回落到默认源。
// 「回落到默认源」是手机版注释里点名要避免的行为：用户会看到自己的设置凭空变成别的源。
func TestNormalizeLegacyMetadataSourceFoldsBangumiMirror(t *testing.T) {
	for _, input := range []string{"bangumi_mirror", "BANGUMI_MIRROR", " bangumi_mirror "} {
		if got := NormalizeLegacyMetadataSource(input); got != "bangumi" {
			t.Fatalf("NormalizeLegacyMetadataSource(%q) = %q, want %q", input, got, "bangumi")
		}
	}
	if got := NormalizeLegacyMetadataSource("nextmoe"); got != "nextmoe" {
		t.Fatalf("NormalizeLegacyMetadataSource(\"nextmoe\") = %q, want %q", got, "nextmoe")
	}
	// IsSelectableMetadataSource 与手机版 isValidMetadataSource 一样严格判原值：
	// 已下线的镜像源不算可选，要折叠后才能通过。
	if IsSelectableMetadataSource("bangumi_mirror") {
		t.Fatal("bangumi_mirror 不应再是可选的资料源")
	}
	if !IsSelectableMetadataSource(NormalizeLegacyMetadataSource("bangumi_mirror")) {
		t.Fatal("折叠成 bangumi 后应通过校验")
	}
}

func TestDefaultCurrentMetadataSourceIsNextMoe(t *testing.T) {
	// 默认源与手机版一致是 NextMoe（匿名即可调用，且数据是六源对齐的）。
	if DefaultCurrentMetadataSource != enums2.NextMoe {
		t.Fatalf("expected default current metadata source to be NextMoe, got %q", DefaultCurrentMetadataSource)
	}
}

func TestNormalizeCurrentMetadataSourceFoldsLegacyMirror(t *testing.T) {
	config := &AppConfig{CurrentMetadataSource: enums2.BangumiMirror}
	if !NormalizeCurrentMetadataSource(config) {
		t.Fatal("expected current metadata source to be normalized")
	}
	// 关键：落到 bangumi 而不是默认源 NextMoe —— 用户的显式选择不能被改写意图。
	if config.CurrentMetadataSource != enums2.Bangumi {
		t.Fatalf("expected Bangumi, got %q", config.CurrentMetadataSource)
	}
}

func TestNormalizeCurrentMetadataSourceKeepsExplicitVNDB(t *testing.T) {
	config := &AppConfig{CurrentMetadataSource: enums2.VNDB}
	if NormalizeCurrentMetadataSource(config) {
		t.Fatal("VNDB 是合法选择，不应被改写")
	}
	if config.CurrentMetadataSource != enums2.VNDB {
		t.Fatalf("expected VNDB, got %q", config.CurrentMetadataSource)
	}
}

func TestNormalizeMetadataCoverSourcesDefaultsToHikarinagi(t *testing.T) {
	config := &AppConfig{
		BangumiCoverSource: "unknown",
	}

	NormalizeMetadataCoverSources(config)

	if config.BangumiCoverSource != enums2.MetadataCoverSourceHikarinagi {
		t.Fatalf("expected Bangumi cover source to default to Hikarinagi, got %q", config.BangumiCoverSource)
	}
	if config.VNDBCoverSource != enums2.MetadataCoverSourceHikarinagi {
		t.Fatalf("expected VNDB cover source to default to Hikarinagi, got %q", config.VNDBCoverSource)
	}
}

func TestNormalizeMetadataCoverSourcesKeepsOriginal(t *testing.T) {
	config := &AppConfig{
		BangumiCoverSource: enums2.MetadataCoverSourceOriginal,
		VNDBCoverSource:    enums2.MetadataCoverSourceOriginal,
	}

	NormalizeMetadataCoverSources(config)

	if config.BangumiCoverSource != enums2.MetadataCoverSourceOriginal || config.VNDBCoverSource != enums2.MetadataCoverSourceOriginal {
		t.Fatalf("expected original cover sources to remain unchanged: %+v", config)
	}
}

func TestNormalizeProxySettingsKeepsNetworkProxyURLAsGlobalURL(t *testing.T) {
	config := &AppConfig{
		NetworkProxyMode: "manual",
		NetworkProxyURL:  " 127.0.0.1:7890 ",
	}

	if !NormalizeProxySettings(config) {
		t.Fatal("expected proxy normalization to report changes")
	}
	if config.NetworkProxyURL != "127.0.0.1:7890" {
		t.Fatalf("expected global proxy URL to be trimmed, got %q", config.NetworkProxyURL)
	}
	if config.NetworkProxyMode != "manual" {
		t.Fatalf("unexpected proxy mode: %q", config.NetworkProxyMode)
	}
}

func TestNetworkProxyConfigReturnsGlobalProxy(t *testing.T) {
	config := &AppConfig{
		NetworkProxyMode: "manual",
		NetworkProxyURL:  "http://127.0.0.1:7890",
	}

	mode, proxyURL := config.NetworkProxyConfig()
	if mode != "manual" || proxyURL != config.NetworkProxyURL {
		t.Fatalf("unexpected network proxy config: mode=%q url=%q", mode, proxyURL)
	}
}

func TestSanitizeUmbraConfigPreservesConfiguredBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "stage service",
			baseURL: "https://stage.umbrae.cc",
			want:    "https://stage.umbrae.cc",
		},
		{
			name:    "custom service with surrounding whitespace",
			baseURL: " https://umbra.example.com/// ",
			want:    "https://umbra.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &AppConfig{
				UmbraBaseURL:       tt.baseURL,
				UmbraAuthenticated: true,
			}

			SanitizeUmbraConfig(config)
			if config.UmbraBaseURL != tt.want {
				t.Fatalf("expected Umbra base URL %q, got %q", tt.want, config.UmbraBaseURL)
			}
			if !config.UmbraAuthenticated {
				t.Fatal("sanitizing a configured Umbra base URL cleared authentication")
			}
		})
	}
}

func TestNormalizeScrapedTagLimitAllowsZeroAndUnlimited(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "too negative becomes unlimited", limit: -2, want: -1},
		{name: "unlimited stays negative one", limit: -1, want: -1},
		{name: "zero disables scraped tags", limit: 0, want: 0},
		{name: "positive limit is kept", limit: 10, want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeScrapedTagLimit(tt.limit); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNormalizeLocalDBBackupRetention(t *testing.T) {
	tests := []struct {
		name      string
		retention int
		want      int
	}{
		{name: "zero uses default", retention: 0, want: DefaultLocalDBBackupRetention},
		{name: "negative uses default", retention: -1, want: DefaultLocalDBBackupRetention},
		{name: "configured value remains", retention: 8, want: 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeLocalDBBackupRetention(tt.retention); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNormalizeProcessDetectionTimeoutSec(t *testing.T) {
	tests := []struct {
		name       string
		timeoutSec int
		want       int
	}{
		{name: "missing value uses default", timeoutSec: 0, want: DefaultProcessDetectionTimeoutSec},
		{name: "value below minimum is clamped", timeoutSec: 30, want: MinProcessDetectionTimeoutSec},
		{name: "valid value is kept", timeoutSec: 180, want: 180},
		{name: "value above maximum is clamped", timeoutSec: 900, want: MaxProcessDetectionTimeoutSec},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeProcessDetectionTimeoutSec(tt.timeoutSec); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNormalizeScheduledDBBackup(t *testing.T) {
	config := &AppConfig{
		ScheduledDBBackupMode:            "unexpected",
		ScheduledDBBackupIntervalMinutes: 3,
		ScheduledDBBackupTime:            "25:99",
	}

	if !NormalizeScheduledDBBackup(config) {
		t.Fatal("expected scheduled database backup settings to be normalized")
	}
	if config.ScheduledDBBackupMode != ScheduledDBBackupModeInterval {
		t.Fatalf("unexpected mode: %q", config.ScheduledDBBackupMode)
	}
	if config.ScheduledDBBackupIntervalMinutes != DefaultScheduledDBBackupIntervalMinutes {
		t.Fatalf("unexpected interval: %d", config.ScheduledDBBackupIntervalMinutes)
	}
	if config.ScheduledDBBackupTime != DefaultScheduledDBBackupTime {
		t.Fatalf("unexpected daily time: %q", config.ScheduledDBBackupTime)
	}
}

func TestNormalizeBigScreenEffectLevel(t *testing.T) {
	tests := []struct {
		name  string
		level string
		want  string
	}{
		{name: "off is kept", level: "off", want: "off"},
		{name: "low is kept", level: "low", want: "low"},
		{name: "high is kept", level: "high", want: "high"},
		{name: "case and spaces are trimmed", level: "  HIGH ", want: "high"},
		{name: "empty uses default", level: "", want: DefaultBigScreenEffectLevel},
		{name: "unknown uses default", level: "ultra", want: DefaultBigScreenEffectLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeBigScreenEffectLevel(tt.level); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestMigrateLegacyCompatibilityConfigMovesCrossOverFields(t *testing.T) {
	config := &AppConfig{
		WineRunnerPath: "/Applications/CrossOver.app/Contents/SharedSupport/CrossOver/bin/wine",
		WinePrefix:     "Legacy Bottle",
	}

	if !MigrateLegacyCompatibilityConfig(config) {
		t.Fatal("expected legacy CrossOver config to be migrated")
	}
	if config.WineRunnerPath != "" || config.WinePrefix != "" {
		t.Fatalf("legacy shared fields were not cleared: path=%q prefix=%q", config.WineRunnerPath, config.WinePrefix)
	}
	if config.CrossOverRunnerPath != "/Applications/CrossOver.app/Contents/SharedSupport/CrossOver/bin/wine" || config.CrossOverBottle != "Legacy Bottle" {
		t.Fatalf("unexpected migrated CrossOver config: path=%q bottle=%q", config.CrossOverRunnerPath, config.CrossOverBottle)
	}
}

func TestMigrateLegacyCompatibilityConfigKeepsWineFields(t *testing.T) {
	config := &AppConfig{
		WineRunnerPath: "/opt/homebrew/bin/wine",
		WinePrefix:     "/Users/test/.wine",
	}

	if MigrateLegacyCompatibilityConfig(config) {
		t.Fatal("plain Wine config should not be migrated")
	}
	if config.WineRunnerPath != "/opt/homebrew/bin/wine" || config.WinePrefix != "/Users/test/.wine" {
		t.Fatalf("Wine config changed unexpectedly: %+v", config)
	}
}
