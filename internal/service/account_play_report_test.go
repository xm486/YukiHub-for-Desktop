package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/service/yukihubaccount"
)

// ------------------------ StartService 侧：结算即上报 ------------------------

// recordingPlayReporter 记账替身：把 StartService 上报的内容原样记下来，
// 供断言「谁被报、报了多长」。
type recordingPlayReporter struct {
	mu    sync.Mutex
	calls []recordedPlayReport
	// observed 每次上报都会收到一次信号（带缓冲，绝不阻塞被测代码）。
	observed chan recordedPlayReport
}

type recordedPlayReport struct {
	SessionID       string
	GameTitle       string
	StartTime       time.Time
	DurationSeconds int
}

func (r *recordingPlayReporter) reportPlaySession(sessionID, gameTitle string, startTime time.Time, durationSeconds int) {
	report := recordedPlayReport{
		SessionID:       sessionID,
		GameTitle:       gameTitle,
		StartTime:       startTime,
		DurationSeconds: durationSeconds,
	}
	r.mu.Lock()
	r.calls = append(r.calls, report)
	r.mu.Unlock()
	select {
	case r.observed <- report:
	default:
	}
}

func (r *recordingPlayReporter) snapshot() []recordedPlayReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedPlayReport(nil), r.calls...)
}

func newRecordingPlayReporter() *recordingPlayReporter {
	return &recordingPlayReporter{observed: make(chan recordedPlayReport, 4)}
}

// waitForReport 等一次上报到达（异步 goroutine，需要等待而不是立刻断言）。
func (r *recordingPlayReporter) waitForReport(t *testing.T) recordedPlayReport {
	t.Helper()
	select {
	case report := <-r.observed:
		return report
	case <-time.After(5 * time.Second):
		t.Fatal("等待游玩时长上报超时：结算后必须上报一次")
		return recordedPlayReport{}
	}
}

// 结算一次两小时的会话 → 必须上报一次，且带上会话 UUID、游戏名与本地结算时长。
func TestFinalizePlaySessionReportsToAccountServer(t *testing.T) {
	startService, _ := setupManualPlaySessionTest(t, &appconf.AppConfig{})
	reporter := newRecordingPlayReporter()
	startService.SetPlaySessionReporter(reporter)

	if _, err := startService.StartManualPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("StartManualPlaySession: %v", err)
	}
	session := startService.getActiveSession(manualSessionTestGameID)
	if session == nil {
		t.Fatal("会话未注册")
	}
	session.startTime = time.Now().Add(-2 * time.Hour)

	if err := startService.EndCurrentPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("EndCurrentPlaySession: %v", err)
	}

	report := reporter.waitForReport(t)
	if report.SessionID != session.sessionID {
		t.Fatalf("上报的 sessionUuid 必须是 play_sessions.id（防重复上报的键），期望 %q，实际 %q",
			session.sessionID, report.SessionID)
	}
	if report.GameTitle != "手动计时测试" {
		t.Fatalf("上报的游戏标题不对：%q", report.GameTitle)
	}
	if report.DurationSeconds < 7100 || report.DurationSeconds > 7210 {
		t.Fatalf("上报时长应约为 7200 秒，实际 %d", report.DurationSeconds)
	}
	// 多报一次都会重复计经验，所以必须是「恰好一次」。
	time.Sleep(200 * time.Millisecond)
	if calls := reporter.snapshot(); len(calls) != 1 {
		t.Fatalf("一次结算只应上报一次，实际 %d 次", len(calls))
	}
}

// 不足 60 秒的会话本地会被删、服务端也不计 → 不应该白发一次请求。
func TestFinalizeSkipsPlayReportForShortSession(t *testing.T) {
	startService, _ := setupManualPlaySessionTest(t, &appconf.AppConfig{})
	reporter := newRecordingPlayReporter()
	startService.SetPlaySessionReporter(reporter)

	if _, err := startService.StartManualPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("StartManualPlaySession: %v", err)
	}
	// 默认 startTime 就是「现在」，结算时不足 60 秒。
	if err := startService.EndCurrentPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("EndCurrentPlaySession: %v", err)
	}

	select {
	case report := <-reporter.observed:
		t.Fatalf("短会话不应上报，却收到 %+v", report)
	case <-time.After(300 * time.Millisecond):
	}
}

// 没注入上报方（例如单测、或未来某个精简形态）时不能 panic / 不能报错。
func TestFinalizeWithoutPlayReporterIsSilent(t *testing.T) {
	startService, db := setupManualPlaySessionTest(t, &appconf.AppConfig{})

	if _, err := startService.StartManualPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("StartManualPlaySession: %v", err)
	}
	startService.getActiveSession(manualSessionTestGameID).startTime = time.Now().Add(-2 * time.Hour)
	if err := startService.EndCurrentPlaySession(manualSessionTestGameID); err != nil {
		t.Fatalf("EndCurrentPlaySession: %v", err)
	}

	// 本地记录照旧落库 —— 上报是附加动作，缺了它不能影响统计。
	if recorded := totalRecordedSeconds(t, db); recorded < 7100 {
		t.Fatalf("缺少上报方时本地仍应正常落库，实际 %d", recorded)
	}
}

// ------------------------ AccountService 侧：请求负载 ------------------------

// 上报负载必须与手机版 SocialApiClient.reportPlayTime 逐字段对齐。
func TestAccountServicePlayReportPayloadMatchesMobileContract(t *testing.T) {
	var (
		mu         sync.Mutex
		path       string
		authHeader string
		payload    map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(raw, &decoded)
		mu.Lock()
		path = r.URL.Path
		authHeader = r.Header.Get("Authorization")
		payload = decoded
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"expAwarded":5,"level":3}`))
	}))
	defer server.Close()

	account := newAccountServiceForPlayReportTest(server.URL, "tok-1")
	startTime := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	account.reportPlaySession("11111111-2222-3333-4444-555555555555", "测试游戏", startTime, 3600)

	mu.Lock()
	defer mu.Unlock()

	if !strings.HasSuffix(path, "/game/play_report") {
		t.Fatalf("上报路径应以 /game/play_report 结尾，实际 %q", path)
	}
	if authHeader != "Bearer tok-1" {
		t.Fatalf("上报必须带 Bearer 令牌，实际 %q", authHeader)
	}
	if got := payload["sessionUuid"]; got != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("sessionUuid 不对：%v", got)
	}
	if got := payload["gameKey"]; got != "" {
		t.Fatalf("gameKey 目前两端都发空串，实际 %v", got)
	}
	if got := payload["title"]; got != "测试游戏" {
		t.Fatalf("title 不对：%v", got)
	}
	// 毫秒时间戳：服务端按毫秒识别并换算成秒。
	if got := payload["startTime"]; got != float64(startTime.UnixMilli()) {
		t.Fatalf("startTime 应为毫秒时间戳 %d，实际 %v", startTime.UnixMilli(), got)
	}
	wantEnd := startTime.Add(time.Hour).UnixMilli()
	if got := payload["endTime"]; got != float64(wantEnd) {
		t.Fatalf("endTime 应为「起始时间 + 本地结算时长」= %d，实际 %v", wantEnd, got)
	}
}

// 未登录时直接跳过：服务端只会回 401，没必要发请求。
func TestAccountServicePlayReportSkipsWhenLoggedOut(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	account := newAccountServiceForPlayReportTest(server.URL, "")
	account.reportPlaySession("session-1", "测试游戏", time.Now().Add(-time.Hour), 3600)

	if requestCount != 0 {
		t.Fatalf("未登录不应发请求，实际发了 %d 次", requestCount)
	}
}

// 不足一分钟的会话不上报（不变量守卫）。
func TestAccountServicePlayReportSkipsShortDuration(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
	}))
	defer server.Close()

	account := newAccountServiceForPlayReportTest(server.URL, "tok-1")
	account.reportPlaySession("session-1", "测试游戏", time.Now().Add(-30*time.Second), 30)

	if requestCount != 0 {
		t.Fatalf("不足 60 秒不应上报，实际发了 %d 次", requestCount)
	}
}

func newAccountServiceForPlayReportTest(baseURL string, accessToken string) *AccountService {
	account := NewAccountService()
	account.ctx = context.Background()
	account.config = &appconf.AppConfig{YukiHubAccountAccessToken: accessToken}
	account.client = yukihubaccount.NewClient(baseURL)
	return account
}
