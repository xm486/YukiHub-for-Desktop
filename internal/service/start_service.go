package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/common/vo"
	"yukihub/internal/models"
	"yukihub/internal/service/cloudprovider"
	launcherpkg "yukihub/internal/service/launcher"
	"yukihub/internal/utils/audioutils"
	"yukihub/internal/utils/processutils"
	"yukihub/internal/utils/timerutils"
	"yukihub/internal/utils/timerutils/focusing"

	"yukihub/internal/wailsruntime"
)

const (
	homeRefreshRequestedEvent = "home:refresh-requested"
	gameRuntimeChangedEvent   = "game-runtime:changed"
	sessionHeartbeatInterval  = 15 * time.Second
)

type GameRuntimeState string

const (
	GameRuntimeStateLaunching GameRuntimeState = "launching"
	GameRuntimeStatePlaying   GameRuntimeState = "playing"
	GameRuntimeStateEnding    GameRuntimeState = "ending"
	GameRuntimeStateIdle      GameRuntimeState = "idle"
)

type GameRuntimeTimingMode string

const (
	GameRuntimeTimingModeWallClock GameRuntimeTimingMode = "wall-clock"
	GameRuntimeTimingModeActive    GameRuntimeTimingMode = "active"
)

type GameRuntimeChangedEvent struct {
	GameID        string                `json:"game_id"`
	Game          *models.Game          `json:"game,omitempty"`
	SessionID     string                `json:"session_id,omitempty"`
	StartTime     time.Time             `json:"start_time,omitempty"`
	State         GameRuntimeState      `json:"state"`
	Reason        string                `json:"reason,omitempty"`
	TimingMode    GameRuntimeTimingMode `json:"timing_mode,omitempty"`
	ActiveSeconds *int                  `json:"active_seconds,omitempty"`
	IsFocused     *bool                 `json:"is_focused,omitempty"`
	// ProcessUnknown 表示这是「进程识别失败」降级出来的会话：计时照常，
	// 但没有进程可监控，结束靠「回到 YukiHub」兜底。前端据此提示用户。
	ProcessUnknown bool `json:"process_unknown,omitempty"`
}

type StartService struct {
	ctx                context.Context
	config             *appconf.AppConfig
	backupService      *BackupService
	gameService        *GameService
	integrationService *IntegrationService
	sessionService     *SessionService
	activeTimeTracker  *timerutils.ActiveTimeTracker
	runtime            wailsruntime.Runtime
	// playReporter 由 main 注入（AccountService）：会话结算后把这次游玩上报
	// 账号服务端，计入游戏经验与网页端每日任务时长。见 account_play_report.go。
	playReporter playSessionReporter

	activeSessions   map[string]*activePlaySession
	activeSessionsMu sync.Mutex

	// setProcessMuted 可注入，便于在不碰真实 COM 的情况下测试焦点状态机。
	// Init 时若为 nil 会补上 audioutils.SetProcessMuted。
	setProcessMuted func(processID uint32, muted bool) (matched bool, err error)

	// 「回到 YukiHub」兜底 watcher：只在存在无进程会话时运行，
	// 用 appForegroundGrace 的宽限判断用户是不是真的回来了。
	foregroundFallbackMu     sync.Mutex
	foregroundFallbackActive bool
}

type launchedProcess struct {
	PID      uint32
	Name     string
	Handle   uintptr
	ExitChan <-chan struct{}
}

// maxProcessHandoffs 限制单次会话内的进程接力次数，防止异常进程链导致会话永不结束。
const maxProcessHandoffs = 5

// processHandoffState 携带进程接力检测所需的上下文。
// 为 nil 时表示该监控路径不启用接力（如 DetectionLauncherOnly 模式）。
type processHandoffState struct {
	launchDir        string
	savedProcessName string
	exitWatch        launcherpkg.ExitWatch
	handoffs         int
	// launcherFallback 标记被监控的进程并非「确认的游戏本体」：它可能是
	// 检测超时后兜底监控的启动器（AGES 等引擎的自启动器正好是这种），或
	// 用户显式声明按启动器监控的进程。这类进程把控制权交给新进程的时刻
	// 完全取决于用户在菜单里停留多久，继任者检测不能按 60 秒启动期裁剪
	// 宽限重试，否则会把手-off 误判成「游戏已关闭」提前结算。
	launcherFallback bool
}

type activePlaySession struct {
	sessionID string
	gameID    string
	startTime time.Time
	game      models.Game
	done      chan struct{}
	finalOnce sync.Once
	// manual 标记这是一次「纯手动计时」会话：没有游戏进程可追踪，
	// 因此即使开启了「仅记录活跃时长」也必须回退到墙钟 —— 否则 activeSeconds
	// 恒为 0，整条会话会在结算时被当成 <60 秒的短会话直接删掉。
	manual bool
	// processUnknown 标记「进程识别失败」降级出来的会话：仍然计时，但没有
	// 进程可监控，结束判定改由「回到 YukiHub」兜底或用户手动结束。
	processUnknown atomic.Bool
	// activeTrackStarted 标记这次会话**真的**启动了活跃窗口计时器。
	//
	// 判断「该不该用活跃时长」只能看它，不能只看 RecordActiveTimeOnly：
	//   - 纯手动计时没有进程，`startGameFocusTracking` 从不被调用；
	//   - 进程识别失败的降级会话走前台兜底，同样不调用它；
	//   - 追踪器自身启动失败时也不会置位。
	// 这三种情况 activeSeconds 恒为 0，若仍按活跃时长结算，整场游玩会被
	// `<60 秒` 规则删掉（真丢记录），前端计时也会冻结在 00:00:00。
	activeTrackStarted atomic.Bool
	// activeSeconds 由活跃窗口计时回调更新，供 15 秒心跳持久化读取。
	activeSeconds atomic.Int64
	// exitTimeNanos 记录被监控进程**真正退出**的时刻（UnixNano；0 = 非进程
	// 退出路径）。继任者检测有数秒宽限，若等检测结束后才取「现在」当
	// endTime，宽限会被误算进游玩时长；结算时优先用它。
	exitTimeNanos atomic.Int64
	// uiIdleEmitted 标记已经提前对前端发过 idle（兜底监控的进程退出时，
	// 岛屿立刻消失、宽限挪到后台）。结算时就不要再发 ending —— 那会把刚
	// 消失的计时岛闪回来一下。
	uiIdleEmitted   atomic.Bool
	audioMu         sync.Mutex
	audioPID        uint32
	audioMuted      bool
	audioStateKnown bool
	audioLastError  string
	// audioStopped 标记会话已进入收尾：此后的焦点回调一律只做「解除静音」，
	// 不允许再把进程静音（否则收尾期间的 in-flight 回调会重新种下残留静音）。
	audioStopped bool
	// audioLastMuteScan 记录上一次**实际执行**静音扫描的时间，用于后台节流。
	audioLastMuteScan time.Time
}

// audioBackgroundRescanInterval 是游戏在后台时重新扫描音频会话的最小间隔。
//
// 焦点回调每秒一次，而一次扫描要走完整 COM 枚举，成本不低；10 秒一次既能覆盖
// 「游戏在后台新建音频流 / 换输出设备」，又不会把 CPU 和 COM 压满。
const audioBackgroundRescanInterval = 10 * time.Second

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func NewStartService() *StartService {
	return &StartService{
		activeSessions: make(map[string]*activePlaySession),
		runtime:        wailsruntime.Unavailable(),
		// activeTimeTracker 将在 Init 时创建
	}
}

//wails:ignore
func (s *StartService) Init(ctx context.Context, db *sql.DB, config *appconf.AppConfig) {
	s.ctx = ctx
	if s.setProcessMuted == nil {
		s.setProcessMuted = audioutils.SetProcessMuted
	}
	// db 不再使用，但保留参数以保持与其他服务的接口一致性
	s.config = config
	// 初始化内部服务
	s.activeTimeTracker = timerutils.NewActiveTimeTracker(ctx, db)
	s.activeTimeTracker.SetUpdateHandler(s.handleActiveTimeUpdate)
	s.activeTimeTracker.SetFocusUpdateHandler(s.handleFocusUpdate)
	if s.activeSessions == nil {
		s.activeSessions = make(map[string]*activePlaySession)
	}
}

//wails:ignore
func (s *StartService) SetRuntime(runtime wailsruntime.Runtime) {
	if runtime != nil {
		s.runtime = runtime
	}
}

// SetBackupService 设置备份服务（用于自动备份）
//
//wails:ignore
func (s *StartService) SetBackupService(backupService *BackupService) {
	s.backupService = backupService
}

// SetGameService 设置游戏服务（用于获取游戏信息）
//
//wails:ignore
func (s *StartService) SetGameService(gameService *GameService) {
	s.gameService = gameService
}

// SetIntegrationService 设置本机平台集成服务。
//
//wails:ignore
func (s *StartService) SetIntegrationService(integrationService *IntegrationService) {
	s.integrationService = integrationService
}

// SetSessionService 设置会话服务（用于管理游玩记录）
//
//wails:ignore
func (s *StartService) SetSessionService(sessionService *SessionService) {
	s.sessionService = sessionService
}

// SetPlaySessionReporter 设置「游玩会话结算后上报服务端」的接收方（账号服务）。
//
// 不上报也不影响本地统计：没登录 / 没注入时整条链路静默跳过。
//
//wails:ignore
func (s *StartService) SetPlaySessionReporter(reporter playSessionReporter) {
	s.playReporter = reporter
}

// StartGameWithTracking 启动游戏并自动追踪游玩时长
// 当游戏进程退出时，自动保存游玩记录到数据库
func (s *StartService) StartGameWithTracking(gameID string) (bool, error) {
	return s.startGame(gameID, launcherpkg.LaunchOptions{})
}

// StartGameWithOptions 使用指定选项启动游戏
// 供 CLI 调用，支持覆盖 LE 和 Magpie 设置
func (s *StartService) StartGameWithOptions(gameID string, options launcherpkg.LaunchOptions) (bool, error) {
	return s.startGame(gameID, options)
}

// HandleProtocolLaunch validates and dispatches a protocol-triggered game launch.
func (s *StartService) HandleProtocolLaunch(req vo.ProtocolLaunchRequest) error {
	gameID := strings.TrimSpace(req.GameID)
	if gameID == "" {
		err := fmt.Errorf("missing required parameter: game_id")
		s.emitProtocolLaunchError("快捷启动失败", err.Error(), "", "", "")
		return err
	}

	if s.gameService == nil {
		err := fmt.Errorf("game service is not initialized")
		s.emitProtocolLaunchError("快捷启动失败", err.Error(), gameID, "", "")
		return err
	}

	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		wrappedErr := fmt.Errorf("failed to resolve target game: %w", err)
		s.emitProtocolLaunchError("未找到该游戏快捷方式对应的游戏记录", err.Error(), gameID, "", "")
		return wrappedErr
	}

	started, err := s.StartGameWithTracking(gameID)
	if err != nil {
		wrappedErr := fmt.Errorf("start game via protocol: %w", err)
		s.emitProtocolLaunchErrorFromError(fmt.Sprintf("启动《%s》失败", game.Name), err, gameID)
		return wrappedErr
	}
	if !started {
		err := fmt.Errorf("game failed to start")
		s.emitProtocolLaunchError(fmt.Sprintf("启动《%s》失败", game.Name), err.Error(), gameID, "", "")
		return err
	}

	return nil
}

// startGame 内部启动方法，支持通过 options 覆盖配置
func (s *StartService) startGame(gameID string, options launcherpkg.LaunchOptions) (bool, error) {
	if s.gameService == nil {
		return false, fmt.Errorf("game service is not initialized")
	}

	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		applog.LogErrorf(s.ctx, "failed to get game path: %v", err)
		return false, fmt.Errorf("failed to get game path: %w", err)
	}
	path := game.Path
	processName := game.ProcessName
	useSteamLaunch := launcherpkg.SupportsSteamLaunch(&game, options)

	if useSteamLaunch {
		if s.integrationService == nil {
			return false, fmt.Errorf("Steam integration service is not initialized")
		}
		steamStatus, statusErr := s.integrationService.GetGameSteamStatus(gameID)
		if statusErr != nil {
			return false, fmt.Errorf("resolve Steam launch identity: %w", statusErr)
		}
		if !steamStatus.Ready {
			return false, fmt.Errorf("此游戏尚未加入 Steam")
		}
		game.SteamLaunchID = steamStatus.LaunchID
		game.SteamLaunchKind = steamStatus.LaunchKind
		game.SteamUserID = steamStatus.UserID
	}

	// 如果未配置路径或配置的是文件夹，则在首次启动时要求用户选择可执行文件并写回游戏路径
	if !useSteamLaunch {
		resolvedPath, resolvedProcessName, cancelled, err := s.resolveExecutablePath(gameID, path, processName)
		if err != nil {
			applog.LogErrorf(s.ctx, "failed to resolve executable path: %v", err)
			return false, fmt.Errorf("failed to resolve executable path: %w", err)
		}
		if cancelled {
			applog.LogInfof(s.ctx, "user cancelled executable selection for game: %s", gameID)
			return false, nil
		}
		path = resolvedPath
		if strings.TrimSpace(resolvedProcessName) != "" {
			processName = resolvedProcessName
		}
		if strings.TrimSpace(processName) == "" {
			processName = filepath.Base(path)
		}
	}
	game.Path = path
	game.ProcessName = processName

	if s.config.AutoRestoreCloudSave && game.SavePath != "" && cloudprovider.IsConfigured(s.config) {
		if s.backupService == nil {
			return false, fmt.Errorf("backup service is not initialized")
		}
		if _, err := s.backupService.RestoreLatestCloudGameBackupIfNewer(gameID); err != nil {
			applog.LogErrorf(s.ctx, "failed to synchronize cloud save before launch: %v", err)
			return false, fmt.Errorf("启动前同步云端存档失败: %w", err)
		}
	}

	strategy, err := launcherpkg.SelectLauncherStrategy(&game, options, s.config)
	if err != nil {
		applog.LogErrorf(s.ctx, "failed to select launcher strategy: %v", err)
		return false, err
	}
	plan, err := strategy.Plan(s.ctx, &game, options)
	if err != nil {
		applog.LogErrorf(s.ctx, "failed to build launch plan: %v", err)
		return false, err
	}
	if strings.TrimSpace(plan.DisplayName) == "" {
		plan.DisplayName = filepath.Base(plan.File)
	}
	if strings.TrimSpace(plan.DetectionDir) == "" {
		plan.DetectionDir = launcherpkg.EffectiveProcessDetectionDir(game.GameDirectory, filepath.Dir(path))
	}
	if strings.TrimSpace(plan.ExitWatch.DetectionDir) == "" {
		plan.ExitWatch.DetectionDir = plan.DetectionDir
	}
	launcherExeName := filepath.Base(plan.File)

	var startedProcess *processutils.StartedProcess
	if plan.RunAsAdmin {
		applog.LogInfof(s.ctx, "Starting game as administrator: %s", gameID)
		startedProcess, err = processutils.StartProcessElevated(plan.File, plan.Args, plan.Dir)
	} else if len(plan.Env) > 0 {
		startedProcess, err = processutils.StartProcessWithEnv(plan.File, plan.Args, plan.Dir, plan.Env)
	} else {
		startedProcess, err = processutils.StartProcess(plan.File, plan.Args, plan.Dir)
	}
	if err != nil {
		applog.LogErrorf(s.ctx, "failed to start game: %v", err)
		return false, fmt.Errorf("failed to start game: %w", err)
	}

	// 如果启用了 Magpie，在游戏启动后启动 Magpie
	if plan.Magpie && s.config.MagpiePath != "" {
		go s.startMagpie()
	}

	if plan.ActiveTrack.Kind == launcherpkg.ActiveTrackWineRootPID && plan.ActiveTrack.RootPID == 0 {
		plan.ActiveTrack.RootPID = startedProcess.PID
	}
	if plan.ActiveTrack.Kind == launcherpkg.ActiveTrackLauncherPID && plan.ActiveTrack.LauncherPID == 0 {
		plan.ActiveTrack.LauncherPID = startedProcess.PID
	}

	launcher := launchedProcess{
		PID:      startedProcess.PID,
		Name:     plan.DisplayName,
		Handle:   startedProcess.Handle,
		ExitChan: startedProcess.ExitChan,
	}

	startTime := time.Now()
	sessionID, err := s.sessionService.CreatePendingSession(gameID, startTime)
	if err != nil {
		processutils.CloseProcessHandle(startedProcess.Handle)
		return false, fmt.Errorf("failed to create play session: %w", err)
	}

	session := s.registerActiveSession(sessionID, gameID, startTime, game, false)
	s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
		GameID:    gameID,
		Game:      &game,
		SessionID: sessionID,
		StartTime: startTime,
		State:     GameRuntimeStateLaunching,
		Reason:    "launched",
	})

	// 手动计时模式（Yuki 式计时）：游戏照常拉起，但**不做任何进程监测** ——
	// 直接进 playing，由用户回到 YukiHub 手动点停止（EndCurrentPlaySession）结算。
	// 这是手机版的口径（Android 上本来也监控不到进程），也彻底绕开「启动器套娃 /
	// 进程名对不上 / 游戏秒退」带来的时长识别问题。句柄不再用于等待退出，直接关掉。
	if s.usesManualTimingMode() {
		s.closeLauncherHandle(launcher)
		s.emitGameRuntimePlaying(session, "manual-timing-started")
		s.requestHomeRefresh()
		applog.LogInfof(s.ctx, "Game %s launched under manual timing mode; process monitoring disabled", gameID)
		return true, nil
	}

	// 启动进程检测和监控 goroutine
	go s.detectAndMonitorProcess(session, launcher, launcherExeName, plan.DetectionDir, processName, plan)

	// pending session 已创建，Home 数据已发生变化，立即通知前端刷新
	s.requestHomeRefresh()

	// 启动成功，返回 true 给前端
	return true, nil
}

// detectAndMonitorProcess 检测实际游戏进程并开始监控。
func (s *StartService) detectAndMonitorProcess(session *activePlaySession, launcher launchedProcess, launcherExeName string, launchDir string, savedProcessName string, plan launcherpkg.LaunchPlan) {
	gameID := session.gameID

	if plan.DetectionMode == launcherpkg.DetectionLauncherOnly {
		s.monitorLauncherOnly(session, launcher, plan)
		return
	}

	processDetectionTimeoutSec := appconf.DefaultProcessDetectionTimeoutSec
	if s.config != nil {
		processDetectionTimeoutSec = appconf.NormalizeProcessDetectionTimeoutSec(s.config.ProcessDetectionTimeoutSec)
	}
	detectionInput := launcherpkg.StagedProcessDetectionInput{
		GameID: gameID,
		Launcher: launcherpkg.LaunchedProcessInfo{
			PID:  launcher.PID,
			Name: launcher.Name,
		},
		LauncherExeName:   launcherExeName,
		LaunchDir:         launchDir,
		SavedProcessName:  savedProcessName,
		DetectionDeadline: session.startTime.Add(time.Duration(processDetectionTimeoutSec) * time.Second),
		Done:              session.done,
	}

	var result launcherpkg.StagedProcessDetectionResult
	if plan.DetectionMode == launcherpkg.DetectionSteamDirectory {
		result = launcherpkg.DetectSteamDirectoryProcess(detectionInput, serviceDetectionLogger{ctx: s.ctx})
	} else {
		result = launcherpkg.DetectStagedProcess(detectionInput, serviceDetectionLogger{ctx: s.ctx})
	}
	select {
	case <-session.done:
		s.closeLauncherHandle(launcher)
		return
	default:
	}

	handoff := &processHandoffState{
		launchDir:        launchDir,
		savedProcessName: savedProcessName,
		exitWatch:        plan.ExitWatch,
	}

	if strings.TrimSpace(result.PersistProcessName) != "" {
		if err := s.updateGameProcessName(gameID, result.PersistProcessName); err != nil {
			applog.LogWarningf(s.ctx, "Failed to update detected process name for game %s: %v", gameID, err)
		} else {
			handoff.savedProcessName = result.PersistProcessName
		}
	}
	if result.CloseLauncherHandle {
		s.closeLauncherHandle(launcher)
	}
	handoff.launcherFallback = result.MonitoredLauncherAsGame
	if result.RequireProcessSelection || result.ProcessID == 0 {
		// 进程识别失败 ≠ 用户没在玩。这里以前直接删会话（deleteShortOrCancelledSession），
		// 于是「启动器套娃 / 进程名对不上 / 游戏秒退」这些情况全变成白玩一场 ——
		// 原分支的老问题。改成降级：保留会话按墙钟继续计时，结束判定交给
		// 「回到 YukiHub」兜底（watchForegroundFallback）或用户手动结束。
		applog.LogWarningf(
			s.ctx,
			"Process detection failed for game %s (requireSelection=%v); keeping the session alive via foreground fallback",
			gameID, result.RequireProcessSelection,
		)
		s.degradeToForegroundTracking(session)
		return
	}

	s.emitGameRuntimePlaying(session, "process-detected")
	s.startGameFocusTracking(session, result.ProcessID, plan.ActiveTrack)

	if result.UseLauncherHandle && launcher.Handle != 0 {
		s.monitorProcessByHandle(session, result.ProcessID, result.ProcessName, launcher.Handle, handoff)
		return
	}
	s.monitorProcessByPID(session, result.ProcessID, result.ProcessName, handoff)
}

type serviceDetectionLogger struct {
	ctx context.Context
}

func (l serviceDetectionLogger) Infof(format string, args ...any) {
	applog.LogInfof(l.ctx, format, args...)
}

func (l serviceDetectionLogger) Warningf(format string, args ...any) {
	applog.LogWarningf(l.ctx, format, args...)
}

func (s *StartService) closeLauncherHandle(launcher launchedProcess) {
	if launcher.Handle == 0 {
		return
	}
	if err := processutils.CloseProcessHandle(launcher.Handle); err != nil {
		applog.LogWarningf(s.ctx, "Failed to close launcher process handle for %s (PID %d): %v", launcher.Name, launcher.PID, err)
	}
}

func (s *StartService) monitorLauncherOnly(session *activePlaySession, launcher launchedProcess, plan launcherpkg.LaunchPlan) {
	s.emitGameRuntimePlaying(session, "launcher-monitoring")
	s.startGameFocusTracking(session, launcher.PID, plan.ActiveTrack)
	var handoff *processHandoffState
	if plan.EnableProcessHandoff {
		handoff = &processHandoffState{
			launchDir:        plan.DetectionDir,
			savedProcessName: session.game.ProcessName,
			exitWatch:        plan.ExitWatch,
			// 显式按启动器监控 ≠ 确认了游戏本体：AGES 这类「exe 即菜单，
			// 点开始后自重启」的引擎也会在这里发生任意时刻的接力。
			launcherFallback: true,
		}
	}
	if launcher.Handle != 0 {
		s.monitorProcessByHandleWithExitWatch(session, launcher.PID, launcher.Name, launcher.Handle, plan.ExitWatch, handoff)
		return
	}
	if launcher.ExitChan != nil {
		exitChan := s.withExitWatch(session, launcher.PID, launcher.Name, launcher.ExitChan, plan.ExitWatch)
		s.waitForProcessExit(session, launcher.Name, launcher.PID, exitChan, handoff)
		return
	}
	s.monitorProcessByPIDWithExitWatch(session, launcher.PID, launcher.Name, plan.ExitWatch, handoff)
}

func (s *StartService) startGameFocusTracking(session *activePlaySession, processID uint32, activeTrack launcherpkg.ActiveTrack) {
	shouldTrackFocusForMute := s.config.MuteGameInBackground && audioutils.IsProcessMuteSupported()
	if !s.config.RecordActiveTimeOnly && !shouldTrackFocusForMute {
		return
	}

	if _, err := s.activeTimeTracker.StartTrackingWithActiveTrack(session.sessionID, session.gameID, processID, activeTrack); err != nil {
		applog.LogWarningf(s.ctx, "Failed to start active time tracking: %v", err)
		return
	}
	session.activeTrackStarted.Store(true)
}

func (s *StartService) persistSelectedProcessName(gameID string, selectedProcessName string) {
	if !launcherpkg.IsPersistableProcessName(selectedProcessName) {
		applog.LogInfof(s.ctx, "Selected non-exe process for game %s will not be persisted as process_name: %s", gameID, selectedProcessName)
		return
	}
	if err := s.updateGameProcessName(gameID, selectedProcessName); err != nil {
		applog.LogWarningf(s.ctx, "Failed to update selected process name for game %s: %v", gameID, err)
	}
}

func (s *StartService) emitProtocolLaunchError(message string, detail string, gameID string, kind string, configKey string) {
	if s.ctx == nil {
		return
	}
	s.runtime.Emit("protocol-launch:error", vo.ProtocolLaunchErrorEvent{
		Message:   strings.TrimSpace(message),
		Detail:    strings.TrimSpace(detail),
		GameID:    strings.TrimSpace(gameID),
		Kind:      strings.TrimSpace(kind),
		ConfigKey: strings.TrimSpace(configKey),
	})
}

func (s *StartService) emitProtocolLaunchErrorFromError(message string, err error, gameID string) {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	var strategyErr *launcherpkg.StrategyError
	if errors.As(err, &strategyErr) && strategyErr != nil {
		if strings.TrimSpace(strategyErr.UserMessage) != "" {
			message = strategyErr.UserMessage
		}
		s.emitProtocolLaunchError(message, detail, gameID, strategyErr.Kind, strategyErr.ConfigKey)
		return
	}
	s.emitProtocolLaunchError(message, detail, gameID, "", "")
}

// monitorProcessByPID 通过PID监控外部进程直到退出。
// 优先使用平台原生退出通知；不可用时退回进程快照轮询。
func (s *StartService) monitorProcessByPID(session *activePlaySession, processID uint32, processName string, handoff *processHandoffState) {
	exitWatch := launcherpkg.ExitWatch{}
	if handoff != nil {
		exitWatch = handoff.exitWatch
	}
	s.monitorProcessByPIDWithExitWatch(session, processID, processName, exitWatch, handoff)
}

func (s *StartService) monitorProcessByPIDWithExitWatch(session *activePlaySession, processID uint32, processName string, exitWatch launcherpkg.ExitWatch, handoff *processHandoffState) {
	applog.LogInfof(s.ctx, "Starting to monitor external process %s (PID %d) using native process exit notification", processName, processID)

	// 创建进程监控器
	pm, exitChan, err := processutils.WaitForProcessExitAsync(processID)
	if err != nil {
		applog.LogWarningf(s.ctx, "Failed to open process monitor for %s (PID %d), falling back to process snapshot polling: %v", processName, processID, err)
		snapshotMonitor, snapshotExitChan := processutils.WaitForProcessExitBySnapshotAsync(processID)
		defer snapshotMonitor.Stop()
		s.waitForProcessExit(session, processName, processID, s.withExitWatch(session, processID, processName, snapshotExitChan, exitWatch), handoff)
		return
	}
	defer pm.Stop()

	s.waitForProcessExit(session, processName, processID, s.withExitWatch(session, processID, processName, exitChan, exitWatch), handoff)
}

func (s *StartService) monitorProcessByHandle(session *activePlaySession, processID uint32, processName string, processHandle uintptr, handoff *processHandoffState) {
	exitWatch := launcherpkg.ExitWatch{}
	if handoff != nil {
		exitWatch = handoff.exitWatch
	}
	s.monitorProcessByHandleWithExitWatch(session, processID, processName, processHandle, exitWatch, handoff)
}

func (s *StartService) monitorProcessByHandleWithExitWatch(session *activePlaySession, processID uint32, processName string, processHandle uintptr, exitWatch launcherpkg.ExitWatch, handoff *processHandoffState) {
	applog.LogInfof(s.ctx, "Starting to monitor launched process %s (PID %d) using ShellExecuteEx handle", processName, processID)

	pm, exitChan, err := processutils.WaitForProcessHandleExitAsync(processID, processHandle)
	if err != nil {
		applog.LogWarningf(s.ctx, "Failed to monitor process handle for %s (PID %d), falling back to PID monitor: %v", processName, processID, err)
		s.monitorProcessByPIDWithExitWatch(session, processID, processName, exitWatch, handoff)
		return
	}
	defer pm.Stop()

	s.waitForProcessExit(session, processName, processID, s.withExitWatch(session, processID, processName, exitChan, exitWatch), handoff)
}

func (s *StartService) withExitWatch(session *activePlaySession, processID uint32, processName string, exitChan <-chan struct{}, exitWatch launcherpkg.ExitWatch) <-chan struct{} {
	watchChan, ok := launcherpkg.StartExitWatch(launcherpkg.ExitWatchInput{
		RootPID:     processID,
		ProcessName: processName,
		SessionID:   session.sessionID,
		Config:      exitWatch,
		Done:        session.done,
	}, serviceDetectionLogger{ctx: s.ctx})
	if !ok {
		return exitChan
	}

	combined := make(chan struct{})
	go func() {
		select {
		case <-exitChan:
			close(combined)
		case <-watchChan:
			close(combined)
		case <-session.done:
		}
	}()
	return combined
}

func (s *StartService) waitForProcessExit(session *activePlaySession, processName string, processID uint32, exitChan <-chan struct{}, handoff *processHandoffState) {
	// 等待进程退出或超时（24小时）
	select {
	case <-exitChan:
		applog.LogInfof(s.ctx, "Game process %s (PID %d) has exited", processName, processID)
		// 先记下真实退出时刻：继任者检测有数秒宽限，等检测结束后再取
		// 「现在」当 endTime 会把宽限算进游玩时长。
		session.exitTimeNanos.Store(time.Now().UnixNano())
		// 先解除静音：继任者检测最多要等几秒，不能把静音状态拖到那之后。
		s.restoreSessionAudio(session)
		if handoff != nil && handoff.launcherFallback {
			// 兜底监控的进程退出：UI **立刻**放行（idle → 计时岛马上消失，
			// 退游戏零延迟），完整宽限挪到后台找自重启的接力进程，找到就把
			// 同一场会话续上。同步等宽限会让每次退游戏凭空卡好几秒。
			session.uiIdleEmitted.Store(true)
			s.emitGameRuntimeIdle(session, "launcher-exit-handoff-watch")
			s.requestHomeRefresh()
			s.watchLauncherHandoff(session, processID, processName, handoff)
			return
		}
		// 进程退出不一定是游戏结束：彩窗/启动器可能已把控制权交给了新进程
		// （spawn 子进程后自退、同名 re-exec 等），先做一轮继任者检测。
		// 确认过身份的游戏进程只做一次即时检查，不拖慢正常退出。
		if successor, ok := s.detectSuccessorProcess(session, processID, processName, handoff); ok {
			s.continueMonitoringSuccessor(session, successor, handoff)
			return
		}
	case <-session.done:
		applog.LogInfof(s.ctx, "Game runtime tracking for %s was stopped manually", session.gameID)
		return
	case <-time.After(24 * time.Hour):
		applog.LogWarningf(s.ctx, "Game %s exceeded maximum runtime (24h), forcing cleanup", session.gameID)
	}

	// 执行统一的会话清理逻辑
	s.finalizePlaySession(session, "process-exited")
}

// watchLauncherHandoff 在兜底监控的进程退出后，于后台宽限窗内继续找自重启的
// 接力进程（AGES 等引擎：菜单进程退出后真身才迟迟出现）。
//
// UI 不等这个窗口：退出瞬间已经发过 idle，岛屿早就消失了。宽限结束时——
//   - 找到接力且本会话仍是当前注册会话 → 续接（岛屿重新出现，计时连续）；
//   - 否则 → 真正结算，endTime 用进程真实退出时刻，宽限不计入时长。
func (s *StartService) watchLauncherHandoff(session *activePlaySession, exitedPID uint32, exitedName string, handoff *processHandoffState) {
	go func() {
		successor, ok := s.detectSuccessorProcess(session, exitedPID, exitedName, handoff)
		if ok && s.getActiveSession(session.gameID) == session {
			// 宽限期间用户可能重新启动了游戏（注册表已换成新会话）：
			// 旧会话安静结算，别去抢新会话的 UI。
			s.continueMonitoringSuccessor(session, successor, handoff)
			return
		}
		s.finalizePlaySession(session, "process-exited")
	}()
}

// detectSuccessorProcess 在被监控进程退出后，于短暂宽限期内寻找接管的游戏进程。
func (s *StartService) detectSuccessorProcess(session *activePlaySession, exitedPID uint32, exitedName string, handoff *processHandoffState) (processutils.ProcessInfo, bool) {
	if handoff == nil {
		return processutils.ProcessInfo{}, false
	}
	if handoff.handoffs >= maxProcessHandoffs {
		applog.LogWarningf(s.ctx, "Game %s reached process hand-off limit (%d), finalizing session", session.gameID, maxProcessHandoffs)
		return processutils.ProcessInfo{}, false
	}

	input := launcherpkg.SuccessorDetectionInput{
		GameID:                      session.gameID,
		ExitedPID:                   exitedPID,
		ExitedProcessName:           exitedName,
		LaunchDir:                   handoff.launchDir,
		SavedProcessName:            handoff.savedProcessName,
		SessionStart:                session.startTime,
		SelfPID:                     uint32(os.Getpid()),
		MonitoredIsLauncherFallback: handoff.launcherFallback,
	}
	return launcherpkg.DetectSuccessorProcess(input, serviceDetectionLogger{ctx: s.ctx})
}

// continueMonitoringSuccessor 把会话的追踪与监控切换到继任进程上。
func (s *StartService) continueMonitoringSuccessor(session *activePlaySession, successor processutils.ProcessInfo, handoff *processHandoffState) {
	// 继任检测有数秒宽限期，期间会话可能已被手动结束，此时不能再接力。
	select {
	case <-session.done:
		applog.LogInfof(s.ctx, "Game %s session ended during successor detection, skipping hand-off to %s (PID %d)", session.gameID, successor.Name, successor.PID)
		return
	default:
	}

	handoff.handoffs++
	applog.LogInfof(s.ctx, "Game %s process hand-off #%d: continuing session with %s (PID %d)", session.gameID, handoff.handoffs, successor.Name, successor.PID)

	// 只换绑已存在的追踪，不新建：若会话在此期间被结束，新建的追踪将无人回收。
	s.restoreSessionAudio(session)
	if s.activeTimeTracker.IsTracking(session.gameID) {
		s.activeTimeTracker.RetargetTracking(session.gameID, successor.PID)
	}

	// 记住真实游戏进程名，下次启动可直接命中 saved process_name 快捷路径。
	if name := launcherpkg.ProcessNameForPersistence("", successor.Name); name != "" && !strings.EqualFold(name, handoff.savedProcessName) {
		if err := s.updateGameProcessName(session.gameID, name); err != nil {
			applog.LogWarningf(s.ctx, "Failed to persist successor process name for game %s: %v", session.gameID, err)
		} else {
			handoff.savedProcessName = name
		}
	}

	s.emitGameRuntimePlaying(session, "process-handoff")
	s.monitorProcessByPID(session, successor.PID, successor.Name, handoff)
}

// finalizePlaySession 完成游玩会话的最终处理
// 包括停止追踪、计算时长、更新数据库、自动备份等
func (s *StartService) finalizePlaySession(session *activePlaySession, reason string) {
	session.finalOnce.Do(func() {
		s.finalizePlaySessionOnce(session, reason)
	})
}

func (s *StartService) finalizePlaySessionOnce(session *activePlaySession, reason string) {
	sessionID := session.sessionID
	gameID := session.gameID
	startTime := session.startTime

	close(session.done)
	s.unregisterActiveSession(gameID, sessionID)
	// 结束时间优先取被监控进程的真实退出时刻（进程退出路径在退出瞬间已记下，
	// 继任者检测 / 音频恢复的耗时都不该算进游玩时长）；手动结束、前台兜底、
	// 24h 超时没有这个时刻，退回「现在」。
	endTime := time.Now()
	if nanos := session.exitTimeNanos.Load(); nanos != 0 {
		endTime = time.Unix(0, nanos)
	}
	s.stopSessionAudio(session)

	// 确保停止追踪（无论如何都要执行）
	activeSeconds := s.activeTimeTracker.StopTracking(gameID)

	// 兜底路径已在进程退出瞬间发过 idle（岛屿已消失），这里再发 ending 会
	// 把岛闪回来；其余路径维持「先 ending 后 idle」的原有节奏。
	if !session.uiIdleEmitted.Load() {
		s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
			GameID:        gameID,
			Game:          &session.game,
			SessionID:     sessionID,
			StartTime:     startTime,
			State:         GameRuntimeStateEnding,
			Reason:        reason,
			TimingMode:    s.runtimeTimingMode(session),
			ActiveSeconds: s.runtimeActiveSeconds(session, activeSeconds),
		})
	}

	// 只有真正跑过活跃追踪的会话才用活跃时长；手动计时与进程识别失败的降级会话
	// 都没有追踪器可用，一律回退墙钟（否则会被 <60 秒规则删掉）。
	var duration int
	switch {
	case s.usesActiveTimeTracking(session):
		duration = activeSeconds
		applog.LogInfof(s.ctx, "Game %s active play time: %d seconds", gameID, duration)
	default:
		duration = int(endTime.Sub(startTime).Seconds())
		applog.LogInfof(s.ctx, "Game %s total runtime: %d seconds", gameID, duration)
	}

	// 如果游玩时长小于1分钟，删除临时会话记录
	if duration < 60 {
		err := s.sessionService.DeletePlaySession(sessionID)
		if err != nil {
			applog.LogErrorf(s.ctx, "Failed to delete short play session %s: %v", sessionID, err)
			s.emitGameRuntimeIdle(session, "short-session-delete-failed")
		} else {
			s.emitGameRuntimeIdle(session, "short-session-deleted")
			s.requestHomeRefresh()
		}
		return
	}

	// 更新会话记录
	playSession := models.PlaySession{
		ID:        sessionID,
		GameID:    gameID,
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  duration,
	}
	err := s.sessionService.UpdatePlaySession(playSession)
	if err != nil {
		applog.LogErrorf(s.ctx, "Failed to update play session %s: %v", sessionID, err)
		s.emitGameRuntimeIdle(session, "session-finalize-failed")
		return
	}

	s.emitGameRuntimeIdle(session, "session-finalized")
	s.requestHomeRefresh()

	// 上报游玩时长到账号服务端（游戏经验 + 网页端每日任务时长）。
	// 上报的是**本地已落库的那条记录**（起始时间 + 结算时长），不是墙钟跨度 ——
	// 「仅记录活跃时长」模式下两者能差出很多，按墙钟发会把每日任务时长虚报。
	s.reportPlaySessionAsync(sessionID, session.game.Name, startTime, duration)

	// 自动备份游戏存档
	if s.config.AutoBackupGameSave && s.backupService != nil {
		s.autoBackupGameSave(gameID)
	}
}

// reportPlaySessionAsync 异步上报一次游玩会话。
//
// 为什么必须异步：这是网络调用，而调用点在「游戏退出 → 界面回到空闲」这条
// 路径上，用户对这里变慢非常敏感（同步等过一次握手就会被投诉）。
// 上报失败只留日志，本地 play_sessions 已经落库，统计不受影响。
func (s *StartService) reportPlaySessionAsync(sessionID, gameTitle string, startTime time.Time, durationSeconds int) {
	reporter := s.playReporter
	if reporter == nil {
		return
	}
	go reporter.reportPlaySession(sessionID, gameTitle, startTime, durationSeconds)
}

// StartManualPlaySession starts a manual play session: it only records time and
// never launches anything.
//
// The existing tracking is a side effect of *launching* a game (the Android app
// does the same via its Activity lifecycle). On Windows that misses every game
// YukiHub did not start itself: third-party launchers, the Steam client, a plain
// double-click, an automated script… none of those can be covered by watching a
// process. This entry point lets the user start the clock by hand instead.
//
// Returns false when the game is already being tracked — that is not an error.
// Ending the session reuses EndCurrentPlaySession, which stops tracking without
// touching the game process (there is none here).
func (s *StartService) StartManualPlaySession(gameID string) (bool, error) {
	gameID = strings.TrimSpace(gameID)
	if gameID == "" {
		return false, fmt.Errorf("game id is required")
	}
	if s.gameService == nil {
		return false, fmt.Errorf("game service is not initialized")
	}
	if s.sessionService == nil {
		return false, fmt.Errorf("session service is not initialized")
	}
	// 已经在计时中：不重复建会话，否则同一游戏会出现两条并发记录，
	// 且停止按钮只会结算其中一条。
	if s.getActiveSession(gameID) != nil {
		return false, nil
	}

	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		applog.LogErrorf(s.ctx, "manual play session: failed to load game %s: %v", gameID, err)
		return false, err
	}

	startTime := time.Now()
	sessionID, err := s.sessionService.CreatePendingSession(gameID, startTime)
	if err != nil {
		applog.LogErrorf(s.ctx, "manual play session: failed to create session: %v", err)
		return false, err
	}

	// registerActiveSession 内部会起心跳 goroutine：即使进程被强杀/崩溃，
	// 已写入的时长也能靠 updated_at 快照兜回来。
	session := s.registerActiveSession(sessionID, gameID, startTime, game, true)

	// 手动计时没有「启动中」阶段，直接进 playing —— 顶部计时岛
	// 与好友侧的「正在玩 XXX」都靠这个事件点亮。
	s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
		GameID:        gameID,
		Game:          &game,
		SessionID:     sessionID,
		StartTime:     startTime,
		State:         GameRuntimeStatePlaying,
		Reason:        "manual-started",
		TimingMode:    s.runtimeTimingMode(session),
		ActiveSeconds: s.runtimeActiveSeconds(session, 0),
	})

	s.requestHomeRefresh()
	applog.LogInfof(s.ctx, "manual play session started: game=%s session=%s", gameID, sessionID)
	return true, nil
}

// EndCurrentPlaySession manually ends YukiHub tracking for the active game.
// It does not terminate the external game process; it finalizes the current
// play session and stops monitoring so later process exit cannot write twice.
func (s *StartService) EndCurrentPlaySession(gameID string) error {
	gameID = strings.TrimSpace(gameID)
	if gameID == "" {
		return fmt.Errorf("game id is required")
	}

	session := s.getActiveSession(gameID)
	if session == nil {
		return fmt.Errorf("没有正在游玩的游戏: %s", gameID)
	}

	s.finalizePlaySession(session, "manual-ended")
	return nil
}

func (s *StartService) registerActiveSession(sessionID string, gameID string, startTime time.Time, game models.Game, manual bool) *activePlaySession {
	session := &activePlaySession{
		sessionID: sessionID,
		gameID:    gameID,
		startTime: startTime,
		game:      game,
		done:      make(chan struct{}),
		manual:    manual,
	}

	s.activeSessionsMu.Lock()
	s.activeSessions[gameID] = session
	s.activeSessionsMu.Unlock()

	go s.persistSessionHeartbeats(session)

	return session
}

func (s *StartService) persistSessionHeartbeats(session *activePlaySession) {
	ticker := time.NewTicker(sessionHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-session.done:
			return
		case heartbeatAt := <-ticker.C:
			duration := int(heartbeatAt.Sub(session.startTime).Seconds())
			if s.usesActiveTimeTracking(session) {
				duration = int(session.activeSeconds.Load())
			}
			if duration < 0 {
				duration = 0
			}

			if s.sessionService == nil {
				continue
			}
			if err := s.sessionService.saveSessionHeartbeat(session.sessionID, duration, heartbeatAt); err != nil {
				applog.LogWarningf(s.ctx, "Failed to save play session heartbeat %s: %v", session.sessionID, err)
			}
		}
	}
}

func (s *StartService) getActiveSession(gameID string) *activePlaySession {
	s.activeSessionsMu.Lock()
	defer s.activeSessionsMu.Unlock()
	return s.activeSessions[gameID]
}

func (s *StartService) unregisterActiveSession(gameID string, sessionID string) {
	s.activeSessionsMu.Lock()
	defer s.activeSessionsMu.Unlock()

	current := s.activeSessions[gameID]
	if current != nil && current.sessionID == sessionID {
		delete(s.activeSessions, gameID)
	}
}

func (s *StartService) activeSessionSnapshot() []*activePlaySession {
	s.activeSessionsMu.Lock()
	defer s.activeSessionsMu.Unlock()

	sessions := make([]*activePlaySession, 0, len(s.activeSessions))
	for _, session := range s.activeSessions {
		sessions = append(sessions, session)
	}
	return sessions
}

// deleteShortOrCancelledSession 丢弃一次刚建立、但确定不该留下的会话。
//
// 注意：进程识别失败**不再**走这里（那会把「用户其实在玩、只是没识别出进程」
// 的记录一并删掉，见 degradeToForegroundTracking）。目前没有调用点，保留给
// 启动流程被取消的场景。
func (s *StartService) deleteShortOrCancelledSession(session *activePlaySession, reason string) {
	session.finalOnce.Do(func() {
		close(session.done)
		s.unregisterActiveSession(session.gameID, session.sessionID)
		if err := s.sessionService.DeletePlaySession(session.sessionID); err != nil {
			applog.LogErrorf(s.ctx, "Failed to delete cancelled play session %s: %v", session.sessionID, err)
		}
		s.stopSessionAudio(session)
		s.activeTimeTracker.StopTracking(session.gameID)
		s.emitGameRuntimeIdle(session, reason)
		s.requestHomeRefresh()
	})
}

// appForegroundGrace 是「回到 YukiHub」判定的宽限期：YukiHub 需要连续处于
// 前台这么久才算用户真的回来了（避免 alt-tab 瞄一眼就把会话结算掉）。
const appForegroundGrace = 25 * time.Second

// foregroundFallbackPollInterval 是兜底 watcher 的轮询间隔。
const foregroundFallbackPollInterval = 5 * time.Second

// degradeToForegroundTracking 把会话降级成「无进程监控」：计时照常走，
// 但不靠进程退出来结束。
func (s *StartService) degradeToForegroundTracking(session *activePlaySession) {
	session.processUnknown.Store(true)
	s.emitGameRuntimePlaying(session, "process-unknown")
	s.watchForegroundFallback()
}

// unknownProcessSessions 返回当前所有「无进程监控」的活跃会话。
func (s *StartService) unknownProcessSessions() []*activePlaySession {
	s.activeSessionsMu.Lock()
	defer s.activeSessionsMu.Unlock()

	sessions := make([]*activePlaySession, 0, len(s.activeSessions))
	for _, session := range s.activeSessions {
		if session.processUnknown.Load() {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

// watchForegroundFallback 保证有一个后台 goroutine 在盯「YukiHub 是否回到前台」。
//
// 手机版的计时闭环靠 Activity 生命周期：离开 App 开始、回到 App 结算，完全不
// 依赖进程（手机上本来也监控不到进程）。桌面端「进程识别失败」的会话正好缺
// 这个信号，于是用「YukiHub 自己连续处于前台」作为等价物：用户回到 YukiHub
// 并停留够久，就说明他已经离开游戏了。
//
// 只在存在无进程会话时运行，全部结算完自动退出。
func (s *StartService) watchForegroundFallback() {
	s.foregroundFallbackMu.Lock()
	if s.foregroundFallbackActive {
		s.foregroundFallbackMu.Unlock()
		return
	}
	s.foregroundFallbackActive = true
	s.foregroundFallbackMu.Unlock()

	go func() {
		defer func() {
			s.foregroundFallbackMu.Lock()
			s.foregroundFallbackActive = false
			s.foregroundFallbackMu.Unlock()
		}()

		// ctx 为 nil（测试）时让 done 保持 nil，select 会永久阻塞该分支
		var done <-chan struct{}
		if s.ctx != nil {
			done = s.ctx.Done()
		}

		selfPID := uint32(os.Getpid())
		ticker := time.NewTicker(foregroundFallbackPollInterval)
		defer ticker.Stop()

		var foregroundSince time.Time
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				pending := s.unknownProcessSessions()
				if len(pending) == 0 {
					return
				}

				foregroundPID, ok := focusing.GetForegroundProcessID()
				if !ok || foregroundPID != selfPID {
					// 游戏还在前台（或取不到）→ 重新计时宽限
					foregroundSince = time.Time{}
					continue
				}
				if foregroundSince.IsZero() {
					foregroundSince = now
					continue
				}
				if now.Sub(foregroundSince) < appForegroundGrace {
					continue
				}

				for _, session := range pending {
					applog.LogInfof(
						s.ctx,
						"Finalizing process-unknown session %s: app has been in foreground long enough",
						session.sessionID,
					)
					s.finalizePlaySession(session, "foreground-return")
				}
				foregroundSince = time.Time{}
			}
		}
	}()
}

func (s *StartService) emitGameRuntimePlaying(session *activePlaySession, reason string) {
	s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
		GameID:         session.gameID,
		Game:           &session.game,
		SessionID:      session.sessionID,
		StartTime:      session.startTime,
		State:          GameRuntimeStatePlaying,
		Reason:         reason,
		TimingMode:     s.runtimeTimingMode(session),
		ActiveSeconds:  s.runtimeActiveSeconds(session, 0),
		ProcessUnknown: session.processUnknown.Load(),
	})
}

func (s *StartService) emitGameRuntimeIdle(session *activePlaySession, reason string) {
	s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
		GameID:    session.gameID,
		Game:      &session.game,
		SessionID: session.sessionID,
		StartTime: session.startTime,
		State:     GameRuntimeStateIdle,
		Reason:    reason,
	})
}

func (s *StartService) emitGameRuntimeChanged(event GameRuntimeChangedEvent) {
	if s.ctx == nil {
		return
	}
	s.runtime.Emit(gameRuntimeChangedEvent, event)
}

func (s *StartService) handleActiveTimeUpdate(update timerutils.ActiveTimeUpdate) {
	session := s.getActiveSession(update.GameID)
	if session == nil || session.sessionID != update.SessionID {
		return
	}
	if s.config == nil || !s.config.RecordActiveTimeOnly {
		return
	}
	session.activeSeconds.Store(int64(update.ActiveSeconds))

	s.emitGameRuntimeChanged(GameRuntimeChangedEvent{
		GameID:        session.gameID,
		Game:          &session.game,
		SessionID:     session.sessionID,
		StartTime:     session.startTime,
		State:         GameRuntimeStatePlaying,
		Reason:        "active-time-updated",
		TimingMode:    GameRuntimeTimingModeActive,
		ActiveSeconds: intPtr(update.ActiveSeconds),
		IsFocused:     boolPtr(update.IsFocused),
	})
}

func (s *StartService) handleFocusUpdate(update timerutils.FocusUpdate) {
	if !audioutils.IsProcessMuteSupported() {
		return
	}

	session := s.getActiveSession(update.GameID)
	if session == nil || session.sessionID != update.SessionID {
		return
	}

	session.audioMu.Lock()
	defer session.audioMu.Unlock()
	if session.audioStopped {
		// 会话已收尾，只允许收尾后的解除静音，禁止重新静音。
		s.restoreSessionAudioLocked(session)
		return
	}
	select {
	case <-session.done:
		s.restoreSessionAudioLocked(session)
		return
	default:
	}

	if s.config == nil || !s.config.MuteGameInBackground {
		s.restoreSessionAudioLocked(session)
		return
	}

	// 进程退出时，焦点追踪器可能会先发出一次“失去前台”通知。
	// Windows 音频会话的生命周期可能晚于进程本身，因此仍需先尝试解除静音。
	if !update.IsFocused && !processutils.IsProcessPresentByPID(update.ProcessID) {
		if session.audioStateKnown && session.audioPID == update.ProcessID {
			s.restoreSessionAudioLocked(session)
		}
		return
	}

	shouldMute := !update.IsFocused
	if session.audioStateKnown && session.audioPID == update.ProcessID && session.audioLastError == "" {
		// 已在前台且当前并没有静音：无事可做。
		// audioMuted 这个守卫不能丢 —— 游戏从后台回前台时 shouldMute 变 false，
		// 但 audioMuted 还是 true，这里若直接 return 就**永远不会解除静音**，
		// 游戏回到前台也没声音（这是加节流时必须一并保住的分支）。
		if !shouldMute && !session.audioMuted {
			return
		}
		// 已经在后台静音：仍然要隔一段时间重扫一次，因为游戏可能在不切换焦点的
		// 情况下重建音频流或换到别的输出设备。但焦点回调是**每秒**无条件发一次
		// （见 timerutils.active_time_tracker），COM 全量枚举很贵 ——
		// 每条会话要两次 QueryInterface + GetProcessID + GetSessionInstanceIdentifier。
		// 不节流的话长时间挂机就是每秒压一次 COM。
		if shouldMute && session.audioMuted && time.Since(session.audioLastMuteScan) < audioBackgroundRescanInterval {
			return
		}
	}

	if session.audioStateKnown && session.audioPID != update.ProcessID {
		s.restoreSessionAudioLocked(session)
		if session.audioStateKnown {
			// 上一个 PID 还没恢复成功时保留它，别把线索丢掉。
			return
		}
	}

	matched, err := s.setProcessMuted(update.ProcessID, shouldMute)
	// 枚举过程中可能先改掉了部分会话、随后才在另一条上报错；这些改动要记下来。
	if matched {
		session.audioPID = update.ProcessID
		// 报错可能发生在部分会话已改完之后，只要没全部确认改成就先按“已静音”记，
		// 交给后续回调继续重试，避免残留静音被当成已恢复。
		session.audioMuted = shouldMute || err != nil
		session.audioStateKnown = true
	}
	if shouldMute {
		session.audioLastMuteScan = time.Now()
	}
	if err != nil {
		s.logAudioErrorLocked(session, update.ProcessID, err)
		return
	}
	if !matched {
		return
	}

	session.audioPID = update.ProcessID
	session.audioMuted = shouldMute
	session.audioStateKnown = true
	session.audioLastError = ""
}

func (s *StartService) restoreSessionAudio(session *activePlaySession) {
	if session == nil || !audioutils.IsProcessMuteSupported() {
		return
	}
	session.audioMu.Lock()
	defer session.audioMu.Unlock()
	s.restoreSessionAudioLocked(session)
}

// stopSessionAudio 在会话收尾时解除静音：先立 audioStopped 挡住 in-flight 的
// 焦点回调，再做几次短重试覆盖瞬时失败（会话刚枚举到、COM 忙等）。
func (s *StartService) stopSessionAudio(session *activePlaySession) {
	if session == nil || !audioutils.IsProcessMuteSupported() {
		return
	}
	session.audioMu.Lock()
	defer session.audioMu.Unlock()
	session.audioStopped = true
	for attempt := 0; attempt < 3; attempt++ {
		s.restoreSessionAudioLocked(session)
		if !session.audioStateKnown {
			return
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (s *StartService) restoreSessionAudioLocked(session *activePlaySession) {
	if !session.audioStateKnown {
		return
	}
	if session.audioMuted {
		matched, err := s.setProcessMuted(session.audioPID, false)
		// matched 优先于 err：一次恢复里可能「这个 PID 的新会话解了、上一代遗留的
		// 死会话解不了」。此时若因为 err 提前返回，audioStateKnown 会永远卡在
		// 「已静音」，之后每次重试都重复同一个结果 —— 状态机彻底死锁。
		if !matched {
			if err != nil {
				s.logAudioErrorLocked(session, session.audioPID, err)
				return
			}
			// 枚举不到会话说明引用已经失效（或换过输出设备），必须报错并保留状态，
			// 静默清状态会留下“以为恢复了、其实还静音着”的残留。
			s.logAudioErrorLocked(session, session.audioPID, fmt.Errorf("no audio session found while restoring process audio"))
			return
		}
		if err != nil {
			s.logAudioErrorLocked(session, session.audioPID, err)
		}
	}
	session.audioPID = 0
	session.audioMuted = false
	session.audioStateKnown = false
	session.audioLastError = ""
}

func (s *StartService) logAudioErrorLocked(session *activePlaySession, processID uint32, err error) {
	message := err.Error()
	if session.audioLastError == message {
		return
	}
	session.audioLastError = message
	applog.LogWarningf(s.ctx, "Failed to update background mute for game %s (PID %d): %v", session.gameID, processID, err)
}

// usesManualTimingMode 判断当前是不是「手动计时 / Yuki 式计时」模式。
//
// 手动计时下启动游戏不做进程监测，会话一直挂着直到用户手动停止；只有
// 「进程监测」模式才走 detectAndMonitorProcess 那套自动结算。
func (s *StartService) usesManualTimingMode() bool {
	return s.config != nil &&
		appconf.NormalizePlayTimingMode(s.config.PlayTimingMode) == appconf.PlayTimingModeManual
}

// usesActiveTimeTracking 判断这次会话该不该用「活跃时长」结算。
//
// 唯一判据是会话自己有没有真的启动活跃窗口计时器（见 activeTrackStarted），
// 而不是只看 RecordActiveTimeOnly 开关。纯手动计时、进程识别失败的降级会话、
// 以及追踪器启动失败的会话都没有活跃时长可用，必须回退墙钟 ——
// 否则 activeSeconds 恒为 0，整条记录会被 `<60 秒` 规则删掉。
func (s *StartService) usesActiveTimeTracking(session *activePlaySession) bool {
	return session != nil &&
		!session.manual &&
		session.activeTrackStarted.Load() &&
		s.config != nil && s.config.RecordActiveTimeOnly
}

// runtimeTimingMode 决定这次会话用墙钟还是活跃时长计时。
// 没有真正启动过活跃追踪的会话一律按墙钟走 —— 否则前端会一直显示 00:00:00，
// 而落库时长也会是 0。
func (s *StartService) runtimeTimingMode(session *activePlaySession) GameRuntimeTimingMode {
	if s.usesActiveTimeTracking(session) {
		return GameRuntimeTimingModeActive
	}
	return GameRuntimeTimingModeWallClock
}

func (s *StartService) runtimeActiveSeconds(session *activePlaySession, activeSeconds int) *int {
	if s.usesActiveTimeTracking(session) {
		return intPtr(activeSeconds)
	}
	return nil
}

func (s *StartService) requestHomeRefresh() {
	if s.ctx == nil {
		return
	}

	s.runtime.Emit(homeRefreshRequestedEvent)
}

// updateGameProcessName 更新游戏的进程名
func (s *StartService) updateGameProcessName(gameID string, processName string) error {
	return s.gameService.UpdateGameProcessName(gameID, processName)
}

// CleanupPendingSessions 清理所有待定的游戏会话。
func (s *StartService) CleanupPendingSessions() {
	activeSessions := s.activeSessionSnapshot()
	activeDurations := make(map[string]int)

	// 停止所有活跃时间追踪
	if s.activeTimeTracker != nil {
		for _, session := range activeSessions {
			s.stopSessionAudio(session)
		}
		activeDurations = s.activeTimeTracker.StopAllTracking()
		applog.LogInfof(s.ctx, "Stopped all active time tracking")
	}

	// 结束当前进程内仍在追踪的会话
	if s.sessionService != nil && len(activeSessions) > 0 {
		endTime := time.Now()
		applog.LogInfof(s.ctx, "Completing %d active play sessions during shutdown", len(activeSessions))
		for _, session := range activeSessions {
			duration := int(endTime.Sub(session.startTime).Seconds())
			// 与心跳/结算同一判据：只有真的跑过活跃追踪的会话才取活跃时长。
			// 旧实现只看配置开关，手动计时与降级会话会被算成 0 秒并直接删除 ——
			// 同一场手动计时的会话「崩溃能恢复、正常退出反而丢」。
			if s.usesActiveTimeTracking(session) {
				if active, ok := activeDurations[session.gameID]; ok {
					duration = active
				}
			}

			session.finalOnce.Do(func() {
				close(session.done)
				s.unregisterActiveSession(session.gameID, session.sessionID)
				if err := s.sessionService.completeUnfinishedSessionWithDuration(session.sessionID, endTime, duration); err != nil {
					applog.LogErrorf(s.ctx, "Failed to complete active session %s during shutdown: %v", session.sessionID, err)
				}
			})
		}
	}

	// 清理数据库中未完成的会话
	if s.sessionService != nil {
		err := s.sessionService.CleanupUnfinishedSessions()
		if err != nil {
			applog.LogErrorf(s.ctx, "Failed to cleanup unfinished sessions: %v", err)
		} else {
			applog.LogInfof(s.ctx, "Successfully cleaned up unfinished sessions")
		}
	}
}

// autoBackupGameSave 自动备份游戏存档
func (s *StartService) autoBackupGameSave(gameID string) {
	// 检查是否设置了存档目录
	game, err := s.gameService.GetGameByID(gameID)
	if err != nil || game.SavePath == "" {
		applog.LogDebugf(s.ctx, "Game %s has no save path configured, skipping auto backup", gameID)
		return
	}

	// 执行备份
	applog.LogInfof(s.ctx, "Auto backing up game save for: %s", gameID)
	backup, err := s.backupService.CreateBackup(gameID)
	if err != nil {
		applog.LogErrorf(s.ctx, "Failed to auto backup game save: %v", err)
		return
	}

	// 如果启用了游戏存档自动上传到云端
	if s.config.AutoUploadSaveToCloud && cloudprovider.IsConfigured(s.config) {
		applog.LogInfof(s.ctx, "Auto uploading backup to cloud: %s", backup.Path)
		err = s.backupService.UploadGameBackupToCloud(gameID, backup.Path)
		if err != nil {
			applog.LogErrorf(s.ctx, "Failed to auto upload backup to cloud: %v", err)
		} else {
			applog.LogInfof(s.ctx, "Successfully uploaded backup to cloud: %s", backup.Path)
		}
	}
	applog.LogInfof(s.ctx, "Auto backup completed for game: %s", gameID)
}

// getGamePathAndProcess 获取游戏路径和已保存的进程名
func (s *StartService) getGamePathAndProcess(gameID string) (path string, processName string, err error) {
	if s.gameService == nil {
		return "", "", fmt.Errorf("game service is not initialized")
	}
	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		return "", "", err
	}
	return game.Path, game.ProcessName, nil
}

// resolveExecutablePath 当路径为空或路径是目录时，引导用户选择可执行文件并保存到游戏配置
func (s *StartService) resolveExecutablePath(gameID string, path string, processName string) (string, string, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		applog.LogInfof(s.ctx, "game path is empty for game %s, prompting executable selection", gameID)
		selection, err := s.gameService.SelectGameExecutable("")
		if err != nil {
			return "", "", false, fmt.Errorf("open executable dialog failed: %w", err)
		}
		return s.saveSelectedExecutablePath(gameID, selection, processName)
	}

	normalizedPath, err := filepath.Abs(filepath.Clean(trimmedPath))
	if err != nil {
		return "", "", false, fmt.Errorf("normalize game path failed: %w", err)
	}

	info, err := os.Stat(normalizedPath)
	if err != nil {
		return "", "", false, fmt.Errorf("stat game path: %w", err)
	}
	if !info.IsDir() {
		return normalizedPath, strings.TrimSpace(processName), false, nil
	}

	selection, err := s.gameService.ResolveExecutablePathForImport(normalizedPath)
	if err != nil {
		return "", "", false, fmt.Errorf("open executable dialog failed: %w", err)
	}
	if selection == "" {
		return "", "", true, nil
	}

	return s.saveSelectedExecutablePath(gameID, selection, processName)
}

func (s *StartService) saveSelectedExecutablePath(gameID string, selection string, processName string) (string, string, bool, error) {
	if s.gameService == nil {
		return "", "", false, fmt.Errorf("game service is not initialized")
	}

	selection = strings.TrimSpace(selection)
	if selection == "" {
		return "", "", true, nil
	}

	resolvedSelection, err := filepath.Abs(filepath.Clean(selection))
	if err != nil {
		return "", "", false, fmt.Errorf("normalize selected executable failed: %w", err)
	}
	selectionInfo, err := os.Stat(resolvedSelection)
	if err != nil {
		return "", "", false, fmt.Errorf("stat selected executable failed: %w", err)
	}
	if selectionInfo.IsDir() {
		return "", "", false, fmt.Errorf("selected path is a directory, not executable")
	}

	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		return "", "", false, fmt.Errorf("failed to load game for path update: %w", err)
	}

	resolvedProcessName := strings.TrimSpace(processName)
	if resolvedProcessName == "" {
		resolvedProcessName = filepath.Base(resolvedSelection)
	}

	game.Path = resolvedSelection
	if strings.TrimSpace(game.ProcessName) == "" {
		game.ProcessName = resolvedProcessName
	}
	if err := s.gameService.UpdateGame(game); err != nil {
		return "", "", false, fmt.Errorf("failed to save selected executable: %w", err)
	}

	return resolvedSelection, resolvedProcessName, false, nil
}

// getGameLaunchConfig 获取游戏的启动配置
func (s *StartService) getGameLaunchConfig(gameID string) (useLE bool, useMagpie bool, err error) {
	game, err := s.gameService.GetGameByID(gameID)
	if err != nil {
		return false, false, err
	}
	return game.UseLocaleEmulator, game.UseMagpie, nil
}

// startMagpie 启动 Magpie 程序
func (s *StartService) startMagpie() {
	// 延迟一小段时间，确保游戏窗口已经创建
	time.Sleep(1 * time.Second)

	// 检查 Magpie 是否已经在运行
	isRunning, err := processutils.CheckIfProcessRunning("Magpie.exe")
	if err != nil {
		applog.LogErrorf(s.ctx, "Failed to check Magpie process: %v", err)
		return
	}

	if isRunning {
		applog.LogInfof(s.ctx, "Magpie is already running")
		return
	}

	// 启动 Magpie (tray 模式)
	applog.LogInfof(s.ctx, "Starting Magpie in tray mode: %s", s.config.MagpiePath)
	cmd := exec.Command(s.config.MagpiePath, "-t")
	cmd.Dir = filepath.Dir(s.config.MagpiePath)

	if err := cmd.Start(); err != nil {
		applog.LogErrorf(s.ctx, "Failed to start Magpie: %v", err)
		return
	}

	// 分离进程，避免阻塞
	if cmd.Process != nil {
		cmd.Process.Release()
	}

	applog.LogInfof(s.ctx, "Magpie started successfully")
}
