package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"yukihub/internal/applog"
	"yukihub/internal/common/vo"
	ipccore "yukihub/internal/ipc/core"
	ipcserver "yukihub/internal/ipc/server"
	"yukihub/internal/migrations"
	"yukihub/internal/platform"
	"yukihub/internal/protocol"
	"yukihub/internal/utils"
	"yukihub/internal/utils/apputils"
	"yukihub/internal/utils/dbutils"
	"yukihub/internal/utils/imageutils"
	"yukihub/internal/utils/sessionend"
	"yukihub/internal/utils/winwindow"
	"yukihub/internal/wailsruntime"

	"yukihub/internal/appconf"
	"yukihub/internal/service"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	_ "github.com/duckdb/duckdb-go/v2"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

//go:embed build/windows/tray.png
var windowsTrayIcon []byte

var db *sql.DB

var config *appconf.AppConfig

var appState = newLifecycleState()
var ipcHTTPServer *http.Server
var remoteImageProxyHTTPServer *http.Server
var sessionEndHook *sessionend.Hook

const (
	applicationUniqueID      = "com.yukihub.desktop"
	remoteImageProxyHTTPAddr = "127.0.0.1:23680"
)

type lifecycleState struct {
	ctxMu  sync.RWMutex
	ctx    context.Context
	app    *application.App
	window *application.WebviewWindow

	forceQuit               atomic.Bool
	shuttingDown            atomic.Bool
	systemSessionEnding     atomic.Bool
	quitRequestPending      atomic.Bool
	frontendQuitSyncPlanned atomic.Bool
	frontendQuitSyncRunning atomic.Bool
	frontendQuitSyncBacked  atomic.Bool

	trayAvailable atomic.Bool
}

func newLifecycleState() *lifecycleState {
	return &lifecycleState{}
}

func (s *lifecycleState) SetContext(ctx context.Context) {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	s.ctx = ctx
}

func (s *lifecycleState) SetRuntime(app *application.App, window *application.WebviewWindow) {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	s.app = app
	s.window = window
}

func (s *lifecycleState) Runtime() (*application.App, *application.WebviewWindow) {
	s.ctxMu.RLock()
	defer s.ctxMu.RUnlock()
	return s.app, s.window
}

func (s *lifecycleState) Context() context.Context {
	s.ctxMu.RLock()
	defer s.ctxMu.RUnlock()
	return s.ctx
}

func (s *lifecycleState) IsTrayAvailable() bool {
	return s.trayAvailable.Load() && !s.shuttingDown.Load()
}

func (s *lifecycleState) ShouldForceQuit() bool {
	return s.forceQuit.Load() || s.shuttingDown.Load()
}

func (s *lifecycleState) BeginShutdown() {
	s.shuttingDown.Store(true)
}

func (s *lifecycleState) MarkSystemSessionEnding() {
	s.systemSessionEnding.Store(true)
	s.forceQuit.Store(true)
}

func (s *lifecycleState) IsSystemSessionEnding() bool {
	return s.systemSessionEnding.Load()
}

func (s *lifecycleState) QuitForSystemSessionEnd() {
	s.CaptureWindowState(config)
	s.MarkSystemSessionEnding()

	app, _ := s.Runtime()
	if app == nil || s.shuttingDown.Load() {
		return
	}

	app.Quit()
}

func (s *lifecycleState) HasPendingQuitRequest() bool {
	return s.quitRequestPending.Load()
}

func (s *lifecycleState) ShowMainWindow() {
	if s.shuttingDown.Load() {
		return
	}

	app, window := s.Runtime()
	if app == nil || window == nil {
		return
	}

	window.UnMinimise()
	window.Show()
	window.Focus()
	app.Event.Emit("app:main-window-shown")
}

func (s *lifecycleState) QuitApplication() {
	if s.shuttingDown.Load() {
		return
	}

	app, _ := s.Runtime()
	if app == nil {
		return
	}

	s.CaptureWindowState(config)
	s.forceQuit.Store(true)
	s.shuttingDown.Store(true)
	app.Quit()
}

func (s *lifecycleState) CaptureWindowState(config *appconf.AppConfig) {
	if config == nil {
		return
	}

	_, window := s.Runtime()
	if window == nil {
		return
	}

	config.WindowMaximised = window.IsMaximised()
	if !config.WindowMaximised {
		config.WindowWidth, config.WindowHeight = window.Size()
	}
}

func (s *lifecycleState) ShouldQuitApplication(config *appconf.AppConfig) bool {
	if s.ShouldForceQuit() {
		return true
	}
	if s.HasPendingQuitRequest() {
		return false
	}
	if shouldRunFrontendQuitSync(config) && s.RequestFrontendQuitSync("application-quit") {
		return false
	}

	// Native application quit (for example Cmd+Q) must bypass the window-close
	// hook, which may otherwise interpret shutdown as a close-to-tray request.
	s.CaptureWindowState(config)
	s.forceQuit.Store(true)
	return true
}

func (s *lifecycleState) RequestFrontendQuitSync(reason string) bool {
	if s.shuttingDown.Load() {
		return false
	}

	app, window := s.Runtime()
	if app == nil || window == nil {
		return false
	}

	if !s.quitRequestPending.CompareAndSwap(false, true) {
		return true
	}

	s.frontendQuitSyncPlanned.Store(true)
	s.frontendQuitSyncRunning.Store(false)
	s.frontendQuitSyncBacked.Store(false)
	window.UnMinimise()
	window.Show()
	app.Event.Emit("app:quit-sync-requested", map[string]string{
		"reason": reason,
	})
	return true
}

func (s *lifecycleState) BeginFrontendQuitSyncBackup() {
	s.frontendQuitSyncRunning.Store(true)
}

func (s *lifecycleState) MarkFrontendQuitSyncLocalBackupCreated() {
	s.frontendQuitSyncBacked.Store(true)
}

func (s *lifecycleState) FinishFrontendQuitSyncBackup() {
	s.frontendQuitSyncRunning.Store(false)
}

func (s *lifecycleState) WaitForFrontendQuitSyncBackup(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for s.frontendQuitSyncRunning.Load() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func (s *lifecycleState) ConfigureTray(showStartupErrorPreview func()) {
	app, _ := s.Runtime()
	if app == nil {
		return
	}

	menu := app.NewMenu()
	menu.Add("显示主窗口").OnClick(func(_ *application.Context) {
		s.ShowMainWindow()
	})
	if showStartupErrorPreview != nil {
		menu.AddSeparator()
		menu.Add("开发：预览启动错误窗").OnClick(func(_ *application.Context) {
			showStartupErrorPreview()
		})
	}
	menu.AddSeparator()
	menu.Add("退出").OnClick(func(_ *application.Context) {
		if shouldRunFrontendQuitSync(config) && s.RequestFrontendQuitSync("tray-menu") {
			return
		}
		s.QuitApplication()
	})

	tray := app.SystemTray.New()
	tray.SetMenu(menu)
	tray.SetTooltip("YukiHub")
	// Wails v3 alpha passes a complete ICO container to an API that expects
	// one image resource. Use the extracted 32x32 ICO frame for the tray.
	tray.SetIcon(windowsTrayIcon)
	tray.OnClick(s.ShowMainWindow)
	tray.OnDoubleClick(s.ShowMainWindow)
	s.trayAvailable.Store(true)
}

func shouldRunFrontendQuitSync(config *appconf.AppConfig) bool {
	if config == nil {
		return false
	}

	return config.AutoBackupDB
}

func shouldRunAutomaticCloudSync(config *appconf.AppConfig) bool {
	if config == nil {
		return false
	}

	return config.CloudSyncEnabled && config.AutoCloudSyncEnabled
}

type pendingProtocolRequest struct {
	rawURL  string
	install *vo.InstallRequest
	launch  *vo.ProtocolLaunchRequest
}

func parseProtocolRequest(rawURL string) (*pendingProtocolRequest, error) {
	action, err := protocol.ParseAction(rawURL)
	if err != nil {
		return nil, err
	}

	req := &pendingProtocolRequest{rawURL: rawURL}
	switch action {
	case protocol.ActionInstall:
		installReq, err := protocol.ParseInstallURL(rawURL)
		if err != nil {
			return nil, err
		}
		req.install = installReq
	case protocol.ActionLaunch:
		launchReq, err := protocol.ParseLaunchURL(rawURL)
		if err != nil {
			return nil, err
		}
		req.launch = launchReq
	default:
		return nil, fmt.Errorf("unsupported URL action: %s", action)
	}

	return req, nil
}

func forwardProtocolRequestToRunningInstance(req *pendingProtocolRequest) error {
	switch {
	case req == nil:
		return nil
	case req.install != nil:
		return ipccore.RemoteInstall(req.install)
	case req.launch != nil:
		return ipccore.RemoteLaunch(req.launch)
	default:
		return fmt.Errorf("unsupported protocol request: %s", req.rawURL)
	}
}

func dispatchProtocolRequest(
	req *pendingProtocolRequest,
	downloadService *service.DownloadService,
	startService *service.StartService,
	runtime wailsruntime.Runtime,
	appLogger *applog.FileLogger,
) {
	if req == nil {
		return
	}

	if req.install != nil {
		downloadService.SetPendingInstall(req.install)
		appState.ShowMainWindow()
		runtime.Emit("install:pending", req.install)
		return
	}

	if req.launch != nil {
		launchReq := *req.launch
		go func() {
			// The Wails launch event may arrive before the frontend subscribes to
			// protocol-launch:error, so give the runtime a moment to become ready.
			time.Sleep(1200 * time.Millisecond)
			if err := startService.HandleProtocolLaunch(launchReq); err != nil {
				appLogger.Error("protocol launch failed: " + err.Error())
			}
		}()
	}
}

func repairStaleAppImageProtocolRegistration(appLogger *applog.FileLogger) {
	if goruntime.GOOS != "linux" || !apputils.IsAppImageMode() {
		return
	}

	currentPath, err := apputils.GetLaunchExecutablePath()
	if err != nil {
		appLogger.Warning("failed to resolve AppImage path for protocol repair: " + err.Error())
		return
	}
	registeredPath, err := protocol.GetRegisteredURLSchemeExe()
	if err != nil {
		appLogger.Warning("failed to query protocol registration for AppImage repair: " + err.Error())
		return
	}
	registeredPath = strings.TrimSpace(registeredPath)
	if !protocol.RegistrationNeedsRepair(registeredPath, currentPath) {
		return
	}

	if err := protocol.RegisterPortableURLScheme(currentPath); err != nil {
		appLogger.Warning("failed to repair stale AppImage protocol registration: " + err.Error())
		return
	}
	appLogger.Info(fmt.Sprintf("repaired stale AppImage protocol registration: %s -> %s", registeredPath, currentPath))
}

type startupCoordinator struct {
	startup func(context.Context)
}

// spaWindowRoutes 是按「路由」打开的窗口路径，它们都指向同一份 index.html，
// 再由 main.tsx 按 location.pathname 选择挂载哪个界面。
var spaWindowRoutes = map[string]struct{}{
	"/":        {},
	"/startup": {},
	"/overlay": {},
	"/notice":  {},
}

func frontendAssetHandler(assets fs.FS) http.Handler {
	fileServer := application.AssetFileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wails 的文件服务**没有 SPA fallback**：请求的路径不对就直接 404。
		//
		// 之前只对 /startup 硬编码了改写，于是 /overlay 与 /notice 拿到的是 404
		// 页面（一片白）—— 前端脚本根本没跑，表现是「窗口建出来了、页面也请求了，
		// 但什么都不显示，也没有任何接口调用」。
		//
		// 这里按路由白名单改写。**不要**改成「文件不存在就当 SPA 入口」：
		// embed 的根带着 `frontend/dist/` 前缀，用 assets.Open 判断会永远失败，
		// 结果把所有静态资源也都改写成 index.html（连主界面都白屏）。
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			if _, ok := spaWindowRoutes[r.URL.Path]; ok {
				r = r.Clone(r.Context())
				r.URL.Path = "/index.html"
				r.URL.RawPath = ""
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *startupCoordinator) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.startup(ctx)
	return nil
}

func extractAutostartLaunchFlag(args []string) ([]string, bool) {
	cleanArgs := make([]string, 0, len(args))
	launchedByAutostart := false

	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), wailsruntime.AutostartLaunchArgument) {
			launchedByAutostart = true
			continue
		}
		cleanArgs = append(cleanArgs, arg)
	}

	return cleanArgs, launchedByAutostart
}

// 浮层类窗口（游戏内好友栏 overlay、好友通知浮层）相关常量。
const (
	overlayWindowName   = "overlay"
	overlayWindowWidth  = 380
	overlayWindowHeight = 560
	// 离屏幕右边留一点空隙
	overlayMargin = 24

	// 好友通知浮层：贴在屏幕右下角，尺寸按内容裁紧，不留大块空白。
	noticeWindowName = "notice"
	// 宽度按「头像 + 昵称 + 正在玩 + 较长的游戏名」定：420 时右侧会空出一大片
	// （游戏名短的时候尤其明显），360 既能装下长标题也不会显得空。
	noticeWindowWidth  = 360
	noticeWindowHeight = 88
	noticeMargin       = 20
	// noticeDismissDelay 是通知浮层自动消失前的停留时长。
	// 这里只是兜底（防止前端没跑起来时它一直挂着），正常由前端倒计时收起。
	noticeDismissDelay = 12 * time.Second

	// floatingCornerRadius 是浮层窗口的圆角半径（像素）。
	// Win11 走 DWM 圆角时系统会按 DPI 自行调整，这个值只在退回 SetWindowRgn
	// 裁剪（Win10）时才真正用到。
	floatingCornerRadius = 16
)

// verifyOverlayShortcut 由 main 装配：启动后复查好友栏快捷键有没有真的绑上。
//
// 单独放一个变量是因为它必须在「配置加载之后」才能跑，而配置是在
// ApplicationStarted 回调里面读的，比装配处更早。
var verifyOverlayShortcut func()

var (
	// floatMu 保护下面两个窗口的创建与显隐：快捷键回调、通知轮询、
	// 前端调用都可能并发碰它们。
	floatMu sync.Mutex
	// overlayWindow 是游戏内好友栏；noticeWindow 是好友通知浮层。
	overlayWindow *application.WebviewWindow
	noticeWindow  *application.WebviewWindow
	// activeOverlayShortcut 记录实际注册成功的组合（空 = 都没成功）。
	activeOverlayShortcut string
)

func main() {
	applog.SetMode(applog.ModeCLI)
	const applicationLogLevel = slog.LevelInfo
	logDir, logDirErr := apputils.GetSubDir("logs")
	if logDirErr != nil {
		fmt.Fprintf(os.Stderr, "prepare application log directory failed: %v\n", logDirErr)
		os.Exit(1)
	}
	appLogger := applog.NewFileLogger(filepath.Join(logDir, "app.log"), applicationLogLevel)
	appLogger.Info("application startup initiated")
	for _, runtimeEnv := range platform.ConfigureRuntimeEnvironment() {
		appLogger.Info(fmt.Sprintf("runtime environment configured: %s=%s (%s)", runtimeEnv.Key, runtimeEnv.Value, runtimeEnv.Reason))
	}

	// ================================================================
	// 启动参数预处理：在 Wails 初始化之前处理协议参数
	// ================================================================
	args := os.Args[1:]
	args, launchedByAutostart := extractAutostartLaunchFlag(args)
	var initialProtocolRequest *pendingProtocolRequest

	// yukihub:// URL：检查 GUI 是否已运行
	if len(args) == 1 && protocol.IsProtocolURL(args[0]) {
		req, err := parseProtocolRequest(args[0])
		if err != nil {
			appLogger.Error("failed to parse protocol URL: " + err.Error())
			fmt.Fprintf(os.Stderr, "Error parsing protocol URL: %v\n", err)
			os.Exit(1)
		}
		if ipccore.IsServerRunning() {
			if err := forwardProtocolRequestToRunningInstance(req); err != nil {
				appLogger.Error("failed to forward protocol request to running instance: " + err.Error())
				fmt.Fprintf(os.Stderr, "Error forwarding protocol request to YukiHub: %v\n", err)
				os.Exit(1)
			}
			appLogger.Info("protocol request forwarded to running instance")
			return
		}
		initialProtocolRequest = req
	}

	runGUI(appLogger, applicationLogLevel, launchedByAutostart, initialProtocolRequest)
}
func runGUI(
	appLogger *applog.FileLogger,
	applicationLogLevel slog.Level,
	launchedByAutostart bool,
	initialProtocolRequest *pendingProtocolRequest,
) {
	startupService := service.NewStartupService()
	gameService := service.NewGameService()
	bangumiService := service.NewBangumiService()
	hikarinagiService := service.NewHikarinagiService()
	nextMoeService := service.NewNextMoeService()
	aiService := service.NewAiService()
	aiStatsBuilder := service.NewAIStatsBuilder()
	backupService := service.NewBackupService()
	cloudSyncService := service.NewCloudSyncService()
	homeService := service.NewHomeService()
	statsService := service.NewStatsService()
	startService := service.NewStartService()
	integrationService := service.NewIntegrationService()
	categoryService := service.NewCategoryService()
	configService := service.NewConfigService()
	overlayService := service.NewOverlayService()
	importService := service.NewImportService()
	accountService := service.NewAccountService()
	selfSyncService := service.NewSelfSyncService()
	versionService := service.NewVersionService()
	templateService := service.NewTemplateService()
	updateService := service.NewUpdateService(func() {
		if shouldRunFrontendQuitSync(config) && appState.RequestFrontendQuitSync("application-update") {
			return
		}
		appState.QuitApplication()
	})
	sessionService := service.NewSessionService()
	downloadService := service.NewDownloadService()
	gameProgressService := service.NewGameProgressService()
	gameReviewService := service.NewGameReviewService()
	tagService := service.NewTagService()
	gameFilterPresetService := service.NewGameFilterPresetService()
	mcpReadService := service.NewMCPReadService()
	mcpServerService := service.NewMCPServerService()
	portableSetupService := service.NewPortableSetupService()

	var localFileHandler http.Handler
	var remoteImageProxyHandler http.Handler
	var assetHandlersMu sync.RWMutex
	var mainWindow *application.WebviewWindow
	guiRuntime := wailsruntime.Unavailable()
	var startupReady atomic.Bool
	var startupFailed atomic.Bool
	var initialProtocolRequestHandled atomic.Bool
	var secondInstanceLaunchPending atomic.Bool
	initialProtocolDuplicateSuppressUntil := time.Now().Add(30 * time.Second)
	startupDone := make(chan struct{})

	initBoundServices := func(ctx context.Context) {
		configService.Init(ctx, db, config)
		overlayService.Init(ctx, config)
		// Go controls the first show so the main window cannot cover the
		// startup success state while its frontend is loading.
		configService.SetSuppressInitialWindowShow(true)
		configService.SetQuitHandler(func() {
			if appState.HasPendingQuitRequest() {
				appState.QuitApplication()
				return
			}
			if shouldRunFrontendQuitSync(config) && appState.RequestFrontendQuitSync("frontend-request") {
				return
			}
			appState.QuitApplication()
		})

		downloadService.Init(ctx, db, config)
		gameService.Init(ctx, db, config)
		bangumiService.Init(ctx, db, config)
		hikarinagiService.Init(ctx, db, config)
		nextMoeService.Init(ctx, config)
		tagService.Init(ctx, db, config)
		gameFilterPresetService.Init(ctx, db, config)
		aiService.Init(ctx, db, config)
		aiStatsBuilder.Init(ctx, db, config)
		backupService.Init(ctx, db, config)
		cloudSyncService.Init(ctx, db, config)
		service.ConfigureBackupServiceQuitSyncDBBackupHooks(
			backupService,
			func() { appState.BeginFrontendQuitSyncBackup() },
			func() { appState.MarkFrontendQuitSyncLocalBackupCreated() },
			func() { appState.FinishFrontendQuitSyncBackup() },
		)
		homeService.Init(ctx, db, config)
		statsService.Init(ctx, db, config)
		sessionService.Init(ctx, db, config)
		startService.Init(ctx, db, config)
		integrationService.Init(ctx, db, config)
		categoryService.Init(ctx, db, config)
		importService.Init(ctx, db, config)
		accountService.Init(ctx, db, config, importService)
		selfSyncService.Init(ctx, db, config, importService)
		versionService.Init(ctx)
		templateService.Init(ctx, db, config)
		updateService.Init(ctx)
		gameProgressService.Init(ctx, db, config)
		gameReviewService.Init(ctx, db, config)
		mcpReadService.Init(ctx, db, config)
		mcpServerService.Init(ctx)
		portableSetupService.Init(ctx)

		startService.SetBackupService(backupService)
		startService.SetGameService(gameService)
		startService.SetIntegrationService(integrationService)
		startService.SetSessionService(sessionService)
		downloadService.SetGameService(gameService)
		configService.SetDownloadService(downloadService)
		gameService.SetImageDownloadTaskStarter(downloadService.StartCoverImageDownloadTask)
		gameService.SetTagService(tagService)
		gameService.SetBangumiService(bangumiService)
		gameService.SetHikarinagiService(hikarinagiService)
		gameService.SetNextMoeService(nextMoeService)
		gameReviewService.SetBangumiService(bangumiService)
		gameReviewService.SetHikarinagiService(hikarinagiService)
		importService.SetGameService(gameService)
		integrationService.SetGameService(gameService)
		importService.SetBangumiService(bangumiService)
		importService.SetHikarinagiService(hikarinagiService)
		importService.SetNextMoeService(nextMoeService)
		importService.SetSessionService(sessionService)
		updateService.SetConfigService(configService)
		mcpReadService.SetGameService(gameService)
		mcpReadService.SetStartService(startService)
		mcpReadService.SetSessionService(sessionService)
		mcpReadService.SetGameProgressService(gameProgressService)
		mcpReadService.SetTagService(tagService)
		mcpReadService.SetStatsProvider(aiStatsBuilder)
		mcpServerService.SetReadService(mcpReadService)
		configService.SetConfigUpdateHook(func(updatedConfig appconf.AppConfig) error {
			if err := mcpServerService.ApplyConfig(updatedConfig); err != nil {
				return err
			}
			if err := backupService.EnforceLocalDBBackupRetention(); err != nil {
				applog.LogWarningf(ctx, "failed to enforce local database backup retention: %v", err)
			}
			return nil
		})
	}

	coordinator := &startupCoordinator{
		startup: func(ctx context.Context) {
			appState.SetContext(ctx)
			applog.SetMode(applog.ModeGUI)
			applog.SetLogger(appLogger)
			if os.Getenv("FRONTEND_DEVSERVER_URL") != "" {
				if err := utils.LoadEnvFilesIfExists(".env.build", ".env"); err != nil {
					appLogger.Warning("failed to load dev env files: " + err.Error())
				}
				utils.ApplyDevBuildEnvFallbacks()
			}
		},
	}

	applicationServices := []application.Service{
		application.NewService(coordinator),
		application.NewService(startupService),
		application.NewService(gameService),
		application.NewService(bangumiService),
		application.NewService(hikarinagiService),
		application.NewService(nextMoeService),
		application.NewService(aiService),
		application.NewService(backupService),
		application.NewService(cloudSyncService),
		application.NewService(homeService),
		application.NewService(statsService),
		application.NewService(startService),
		application.NewService(integrationService),
		application.NewService(categoryService),
		application.NewService(configService),
		application.NewService(overlayService),
		application.NewService(importService),
		application.NewService(accountService),
		application.NewService(selfSyncService),
		application.NewService(versionService),
		application.NewService(templateService),
		application.NewService(updateService),
		application.NewService(sessionService),
		application.NewService(downloadService),
		application.NewService(gameProgressService),
		application.NewService(gameReviewService),
		application.NewService(tagService),
		application.NewService(gameFilterPresetService),
		application.NewService(portableSetupService),
	}

	shutdownStartupResources := func() {
		if remoteImageProxyHTTPServer != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = remoteImageProxyHTTPServer.Shutdown(closeCtx)
			remoteImageProxyHTTPServer = nil
		}
		if sessionEndHook != nil {
			sessionEndHook.ReleaseShutdownBlockReason()
			_ = sessionEndHook.Stop()
			sessionEndHook = nil
		}
		if db != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = dbutils.SafeCloseDuckDB(closeCtx, db, appLogger)
			db = nil
		}
	}

	shutdownApplication := func() {
		appState.BeginShutdown()
		if !startupReady.Load() {
			shutdownStartupResources()
			return
		}
		isSystemSessionEnding := appState.IsSystemSessionEnding()
		shutdownMode := "normal"
		if isSystemSessionEnding {
			shutdownMode = "system-session-ending"
		}
		cloudSyncService.StopScheduledSync()
		backupService.StopScheduledDBBackups()

		shutdownStartedAt := time.Now()
		appLogger.Info("shutdown mode: " + shutdownMode)
		logShutdownStep := func(step string, fn func()) {
			stepStartedAt := time.Now()
			appLogger.Info("shutdown step started: " + step)
			fn()
			appLogger.Info(fmt.Sprintf("shutdown step finished: %s (elapsed: %s)", step, time.Since(stepStartedAt)))
		}

		logShutdownStep("shutdown IPC server", func() {
			if err := ipcserver.ShutdownServer(ipcHTTPServer); err != nil {
				appLogger.Error("failed to shutdown IPC server: " + err.Error())
			}
		})
		logShutdownStep("shutdown MCP server", func() {
			if err := mcpServerService.Shutdown(); err != nil {
				appLogger.Error("failed to shutdown MCP server: " + err.Error())
			}
		})
		logShutdownStep("shutdown remote image proxy server", func() {
			if remoteImageProxyHTTPServer == nil {
				return
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := remoteImageProxyHTTPServer.Shutdown(closeCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				appLogger.Error("failed to shutdown remote image proxy server: " + err.Error())
			}
			remoteImageProxyHTTPServer = nil
		})
		logShutdownStep("refresh latest config", func() {
			latestConfig, err := configService.GetAppConfig()
			if err != nil {
				appLogger.Error("failed to get latest config: " + err.Error())
				return
			}
			latestConfig.WindowWidth = config.WindowWidth
			latestConfig.WindowHeight = config.WindowHeight
			latestConfig.WindowMaximised = config.WindowMaximised
			config = &latestConfig
		})
		logShutdownStep("cleanup pending process selections", func() {
			startService.CleanupPendingSessions()
		})
		// 在线状态契约 §3.3：关闭客户端时尽力上报一次下线，别让好友侧一直显示在线。
		// 不上报也会在 10 分钟无心跳后自动判离线，所以这里限时 2 秒、失败只记日志，
		// 不拖长退出；系统注销/关机时不发（系统可能直接掐掉进程，窗口也不够）。
		logShutdownStep("notify presence offline", func() {
			if isSystemSessionEnding {
				return
			}
			offlineCtx, cancel := context.WithTimeout(
				context.Background(),
				2*time.Second,
			)
			defer cancel()
			if err := accountService.NotifyOffline(offlineCtx); err != nil {
				appLogger.Info("presence offline notify skipped: " + err.Error())
			}
		})
		logShutdownStep("release system notification icon", func() {
			accountService.CloseNativeNotifier()
		})
		logShutdownStep("automatic database backup", func() {
			if isSystemSessionEnding || !config.AutoBackupDB {
				return
			}
			if appState.frontendQuitSyncPlanned.Load() {
				if appState.frontendQuitSyncBacked.Load() {
					return
				}
			}
			if _, err := backupService.CreateDBBackupForShutdown(); err != nil {
				appLogger.Error("automatic local database backup failed: " + err.Error())
			}
		})
		logShutdownStep("checkpoint and close database connection", func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dbutils.SafeCloseDuckDB(closeCtx, db, appLogger); err != nil {
				appLogger.Error("database shutdown completed with error: " + err.Error())
			}
			db = nil
		})
		logShutdownStep("save final config", func() {
			if err := appconf.SaveConfig(config); err != nil {
				appLogger.Error("failed to save config: " + err.Error())
			}
		})
		logShutdownStep("shutdown Windows session-end hook", func() {
			if sessionEndHook == nil {
				return
			}
			sessionEndHook.ReleaseShutdownBlockReason()
			if err := sessionEndHook.Stop(); err != nil {
				appLogger.Error("failed to shutdown Windows session-end hook: " + err.Error())
			}
			sessionEndHook = nil
		})
		appLogger.Info(fmt.Sprintf("shutdown completed (total elapsed: %s)", time.Since(shutdownStartedAt)))
	}

	wailsApp := application.New(application.Options{
		Name:        "YukiHub",
		Description: "YukiHub game library manager",
		Icon:        appIcon,
		Logger:      appLogger.Slog(),
		LogLevel:    applicationLogLevel,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: applicationUniqueID,
			OnSecondInstanceLaunch: func(_ application.SecondInstanceData) {
				appLogger.Info("second application launch received")
				secondInstanceLaunchPending.Store(true)
				if startupReady.Load() {
					secondInstanceLaunchPending.Store(false)
					appState.ShowMainWindow()
				}
			},
		},
		Assets: application.AssetOptions{
			Handler: frontendAssetHandler(assets),
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Access-Control-Allow-Origin", "*")
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					if r.Method == http.MethodOptions {
						w.WriteHeader(http.StatusOK)
						return
					}
					assetHandlersMu.RLock()
					localHandler := localFileHandler
					imageHandler := remoteImageProxyHandler
					assetHandlersMu.RUnlock()
					if localHandler != nil && strings.HasPrefix(r.URL.Path, "/local/") {
						localHandler.ServeHTTP(w, r)
						return
					}
					if imageHandler != nil && r.URL.Path == "/proxy/image" {
						imageHandler.ServeHTTP(w, r)
						return
					}
					next.ServeHTTP(w, r)
				})
			},
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		Linux: application.LinuxOptions{
			ApplicationID: applicationUniqueID,
		},
		Services:   applicationServices,
		OnShutdown: shutdownApplication,
		ShouldQuit: func() bool {
			return true
		},
	})

	startupService.SetEventEmitter(func(name string, data ...interface{}) {
		wailsApp.Event.Emit(name, data...)
	})
	newStartupErrorWindow := func(name string, hidden bool) *application.WebviewWindow {
		return wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             name,
			Title:            "YukiHub",
			URL:              "/startup",
			Width:            760,
			Height:           360,
			MinWidth:         760,
			MinHeight:        360,
			MaxWidth:         760,
			MaxHeight:        360,
			AlwaysOnTop:      true,
			Hidden:           hidden,
			DisableResize:    true,
			Frameless:        true,
			InitialPosition:  application.WindowCentered,
			BackgroundType:   application.BackgroundTypeTranslucent,
			BackgroundColour: application.NewRGBA(18, 20, 22, 0),
			Windows: application.WindowsWindow{
				BackdropType: application.Auto,
				Theme:        application.SystemDefault,
			},
			Mac: application.MacWindow{
				TitleBar: application.MacTitleBarHidden,
			},
		})
	}
	startupWindow := newStartupErrorWindow("startup", true)
	startupWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if !startupReady.Load() && !startupFailed.Load() {
			event.Cancel()
			return
		}
		if startupFailed.Load() {
			appState.forceQuit.Store(true)
			go wailsApp.Quit()
		}
	})
	var startupPreviewSequence atomic.Uint64
	var showStartupErrorPreview func()
	if strings.TrimSpace(os.Getenv("FRONTEND_DEVSERVER_URL")) != "" {
		showStartupErrorPreview = func() {
			startupService.ReportFailure(
				"开发预览：数据库启动失败\n\n" +
					"打开数据库失败: IO Error: 无法打开 yukihub.db，文件可能正由另一个进程使用\n\n" +
					"此信息仅用于检查启动错误窗的界面样式。",
			)
			previewWindow := newStartupErrorWindow(
				fmt.Sprintf("startup-preview-%d", startupPreviewSequence.Add(1)),
				true,
			)
			previewWindow.Center()
			previewWindow.Show()
			previewWindow.Focus()
		}
	}

	createMainWindow := func() {
		initWidth := config.WindowWidth
		if initWidth < 970 {
			initWidth = 970
		}
		initHeight := config.WindowHeight
		if initHeight < 563 {
			initHeight = 563
		}
		startState := application.WindowStateNormal
		if config.WindowMaximised {
			startState = application.WindowStateMaximised
		}
		mainWindow = wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "main",
			Title:            "YukiHub",
			URL:              "/",
			Width:            initWidth,
			Height:           initHeight,
			MinWidth:         970,
			MinHeight:        563,
			StartState:       startState,
			Hidden:           true,
			Frameless:        true,
			EnableFileDrop:   true,
			BackgroundType:   application.BackgroundTypeTranslucent,
			BackgroundColour: application.NewRGBA(18, 20, 22, 0),
			Windows: application.WindowsWindow{
				BackdropType: application.Auto,
				Theme:        application.SystemDefault,
			},
			Mac: application.MacWindow{
				TitleBar: application.MacTitleBarHidden,
				Backdrop: application.MacBackdropTranslucent,
			},
		})
		appState.SetRuntime(wailsApp, mainWindow)
		guiRuntime = wailsruntime.New(wailsApp, mainWindow)
		backupService.SetRuntime(guiRuntime)
		bangumiService.SetRuntime(guiRuntime)
		hikarinagiService.SetRuntime(guiRuntime)
		nextMoeService.SetRuntime(guiRuntime)
		cloudSyncService.SetRuntime(guiRuntime)
		configService.SetRuntime(guiRuntime)
		overlayService.SetRuntime(guiRuntime)
		downloadService.SetRuntime(guiRuntime)
		gameService.SetRuntime(guiRuntime)
		importService.SetRuntime(guiRuntime)
		accountService.SetRuntime(guiRuntime)
		selfSyncService.SetRuntime(guiRuntime)
		startService.SetRuntime(guiRuntime)
		statsService.SetRuntime(guiRuntime)
		templateService.SetRuntime(guiRuntime)
		updateService.SetRuntime(guiRuntime)
		appState.ConfigureTray(showStartupErrorPreview)

		mainWindow.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
			wailsApp.Event.Emit("files-dropped", event.Context().DroppedFiles())
		})
		mainWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			appState.CaptureWindowState(config)
			if appState.ShouldForceQuit() {
				return
			}
			if appState.HasPendingQuitRequest() {
				event.Cancel()
				return
			}
			if config.CloseToTray && appState.IsTrayAvailable() {
				mainWindow.Hide()
				event.Cancel()
				return
			}
			if shouldRunFrontendQuitSync(config) && appState.RequestFrontendQuitSync("window-close") {
				event.Cancel()
				return
			}
			event.Cancel()
			go appState.QuitApplication()
		})
	}

	startApplicationServices := func() {
		ctx := appState.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		if err := sessionService.CleanupUnfinishedSessions(); err != nil {
			appLogger.Error("startup cleanup unfinished sessions failed: " + err.Error())
		}
		var sessionHookErr error
		sessionEndHook, sessionHookErr = sessionend.Start(sessionend.Options{
			Reason: "YukiHub 正在保存数据并退出",
			OnQueryEndSession: func() {
				appState.QuitForSystemSessionEnd()
			},
		})
		if sessionHookErr != nil {
			appLogger.Error("failed to start Windows session-end hook: " + sessionHookErr.Error())
		}
		if err := guiRuntime.SetAutostart(config.LaunchAtLogin); err != nil {
			appLogger.Error("failed to sync launch-at-login: " + err.Error())
		}
		if err := mcpServerService.ApplyConfig(*config); err != nil {
			appLogger.Error("failed to apply MCP server config: " + err.Error())
		}
		// 只为 yukihub:// 协议转发保留的本地端点（CLI 已下线）
		ipcHTTPServer = ipcserver.StartServer(ctx, startService, guiRuntime)
		if shouldRunAutomaticCloudSync(config) {
			cloudSyncService.RunStartupSync()
		}
		cloudSyncService.StartScheduledSync()
		selfSyncService.RunStartupSelfSync()
		backupService.StartScheduledDBBackups()
	}

	initializeApplication := func() error {
		ctx := appState.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		loadedConfig, err := appconf.LoadConfig()
		if err != nil {
			return fmt.Errorf("读取应用配置失败: %w", err)
		}
		config = loadedConfig

		repairStaleAppImageProtocolRegistration(appLogger)

		// 好友栏快捷键的复查要等配置读出来才知道用户设的是什么（见下面的
		// verifyOverlayShortcut 装配处）。
		if verifyOverlayShortcut != nil {
			verifyOverlayShortcut()
		}

		// 转区 / 超分工具不随包分发（第三方，各自有许可证，Magpie 还要 .NET 运行时），
		// 所以启动时自动找一遍：用户装过就直接认出来，不用自己去设置里挑路径。
		service.ApplyDetectedCompatTools(config)

		if config.PendingFullRestore != "" || config.PendingDBRestore != "" {
		}
		if config.PendingFullRestore != "" {
			restored, restoreErr := service.ExecuteFullDataRestore(config)
			if restoreErr != nil {
				appLogger.Error("full data restore failed: " + restoreErr.Error())
			} else if restored {
				appLogger.Info("full data restore completed")
			}
		}
		if config.PendingDBRestore != "" {
			restored, restoreErr := service.ExecuteDBRestore(config)
			if restoreErr != nil {
				appLogger.Error("database restore failed: " + restoreErr.Error())
			} else if restored {
				appLogger.Info("database restore completed")
			}
		}

		dataDir, err := apputils.GetDataDir()
		if err != nil {
			return fmt.Errorf("获取应用数据目录失败: %w", err)
		}
		dbPath := filepath.Join(dataDir, "yukihub.db")
		db, err = dbutils.OpenDuckDBWithWALRecovery(ctx, dbPath, appLogger)
		if err != nil {
			return fmt.Errorf("打开数据库失败: %w", err)
		}
		if _, err = db.Exec("SET GLOBAL checkpoint_threshold = '4 MiB'"); err != nil {
			appLogger.Warning("Failed to set DuckDB automatic checkpoint threshold; using the default: " + err.Error())
		}
		timeZone := config.TimeZone
		if timeZone == "" {
			timeZone = "UTC"
		}
		if _, err = db.Exec(fmt.Sprintf("SET TimeZone = '%s'", timeZone)); err != nil {
			appLogger.Warning("Failed to set timezone: " + err.Error())
		}

		if err := migrations.InitSchema(db); err != nil {
			return fmt.Errorf("初始化数据库结构失败: %w", err)
		}
		if err := migrations.Run(ctx, db); err != nil {
			return fmt.Errorf("数据库迁移失败: %w", err)
		}
		if err := migrations.InitIndexes(db); err != nil {
			return fmt.Errorf("初始化数据库索引失败: %w", err)
		}
		if err := dbutils.CheckpointDuckDB(ctx, db); err != nil {
			appLogger.Warning("Database checkpoint after schema initialization failed; committed changes remain in WAL: " + err.Error())
		}

		preparedLocalFileHandler, err := apputils.NewLocalFileHandler()
		if err != nil {
			appLogger.Error("Failed to create local file handler: " + err.Error())
		}
		preparedImageProxyHandler := imageutils.NewRemoteImageProxyHandler(config)
		// 封面源优先度必须作用在代理这一层：前端拿到的是库里存的原始地址
		// （备份导入的 vndb / bgm.tv 地址在国内常常直连不通），只改刮削路径
		// 盖不住这些旧数据。
		preparedImageProxyHandler.SetCoverSourcePreference(config)
		assetHandlersMu.Lock()
		if err == nil {
			localFileHandler = preparedLocalFileHandler
		}
		remoteImageProxyHandler = preparedImageProxyHandler
		assetHandlersMu.Unlock()
		remoteImageProxyListener, listenErr := net.Listen("tcp", remoteImageProxyHTTPAddr)
		if listenErr != nil {
			appLogger.Warning("Failed to start remote image proxy server: " + listenErr.Error())
		} else {
			imageProxyMux := http.NewServeMux()
			imageProxyMux.Handle("/proxy/image", preparedImageProxyHandler)
			remoteImageProxyHTTPServer = &http.Server{Handler: imageProxyMux, ReadHeaderTimeout: 5 * time.Second}
			go func() {
				if serveErr := remoteImageProxyHTTPServer.Serve(remoteImageProxyListener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
					appLogger.Error("Remote image proxy server failed: " + serveErr.Error())
				}
			}()
		}
		initBoundServices(ctx)
		createMainWindow()
		startApplicationServices()
		return nil
	}

	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(_ *application.ApplicationEvent) {
		go func() {
			startupErr := func() (err error) {
				defer func() {
					if recovered := recover(); recovered != nil {
						err = fmt.Errorf("启动期间发生异常: %v\n\n%s", recovered, debug.Stack())
					}
				}()
				return initializeApplication()
			}()
			if startupErr != nil {
				startupFailed.Store(true)
				appLogger.Error("application startup failed: " + startupErr.Error())
				startupService.ReportFailure(startupErr.Error())
				close(startupDone)
				startupWindow.Center()
				startupWindow.Show()
				startupWindow.Focus()
				return
			}
			startupReady.Store(true)
			close(startupDone)
			startupWindow.Close()
			if secondInstanceLaunchPending.Swap(false) || !launchedByAutostart || strings.TrimSpace(config.TimeZone) == "" {
				appState.ShowMainWindow()
			}
			if initialProtocolRequest != nil && initialProtocolRequestHandled.CompareAndSwap(false, true) {
				appLogger.Info("dispatching initial protocol request from startup arguments")
				dispatchProtocolRequest(initialProtocolRequest, downloadService, startService, guiRuntime, appLogger)
			}
		}()
	})
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(event *application.ApplicationEvent) {
		rawURL := event.Context().URL()
		req, err := parseProtocolRequest(rawURL)
		if err != nil {
			appLogger.Error("failed to handle protocol URL: " + err.Error())
			return
		}
		if initialProtocolRequest != nil &&
			req.rawURL == initialProtocolRequest.rawURL &&
			time.Now().Before(initialProtocolDuplicateSuppressUntil) {
			if !initialProtocolRequestHandled.CompareAndSwap(false, true) {
				appLogger.Info("ignored duplicate initial protocol URL event")
				return
			}
			appLogger.Info("dispatching initial protocol request from URL event")
		}
		go func() {
			<-startupDone
			if startupReady.Load() {
				dispatchProtocolRequest(req, downloadService, startService, guiRuntime, appLogger)
			}
		}()
	})

	// ===== 浮层窗口：游戏内好友栏 + 好友通知 =====
	//
	// 两个窗口都惰性创建：不用这些功能的人不该白白多两个 webview。
	//
	// 快捷键**必须在 Run() 之前注册**：这个时机 Wails 只是把它入队，等主线程
	// 消息循环就绪后再真正绑定。若放到 OnStartup 回调里注册，那时内部状态已是
	// 「应用已启动」，会走去主线程同步执行的路径 —— 启动直接被卡死（实测：
	// 进程无任何报错直接退出，日志停在 Platform Info）。
	// clampToZero 把可能为负的坐标压到 0（屏幕比窗口还小时会算出负值）。
	clampToZero := func(value int) int {
		if value < 0 {
			return 0
		}
		return value
	}

	// primaryScreenSize 返回主屏尺寸（拿不到时 ok=false）。
	primaryScreenSize := func() (int, int, bool) {
		screen := wailsApp.Screen.GetPrimary()
		if screen == nil {
			return 0, 0, false
		}
		return screen.Size.Width, screen.Size.Height, true
	}

	// floatingWindowOptions 是浮层窗口的公共外观：无边框 + 置顶 + 半透明，
	// 且不在任务栏/Alt+Tab 里出现（它们是呼出式的浮层，不是独立应用）。
	//
	// X/Y 与 visible 都要在建窗时就定下来 —— **不能先建一个隐藏窗口再 Show()**：
	// Show() 对「刚创建、还没跑起来的窗口」只会触发它的 run() 然后返回
	// （Wails 里 impl == nil 就 InvokeSync(w.Run)），窗口永远不显示。实测就是
	// 这样：日志里能看到浮层的页面被加载了，屏幕上却什么都没有。
	floatingWindowOptions := func(name string, url string, width int, height int, x int, y int, visible bool, background application.RGBA) application.WebviewWindowOptions {
		return application.WebviewWindowOptions{
			Name:   name,
			Title:  "YukiHub",
			URL:    url,
			Width:  width,
			Height: height,
			X:      x,
			Y:      y,
			// 必须显式指定用坐标：InitialPosition 默认是 WindowCentered，
			// 那样 X/Y 会被忽略、窗口跑到屏幕正中间（实测就是这样）。
			InitialPosition: application.WindowXY,
			DisableResize:   true,
			Frameless:       true,
			AlwaysOnTop:     true,
			Hidden:          !visible,
			// 实心背景 + 与卡片同色：此前用 BackgroundTypeTranslucent（走 DWM
			// 背景材质）时，卡片外面那圈留白会被染成紫色描边。
			BackgroundType:   application.BackgroundTypeSolid,
			BackgroundColour: background,
			Windows: application.WindowsWindow{
				BackdropType:    application.Auto,
				Theme:           application.SystemDefault,
				HiddenOnTaskbar: true,
			},
			Mac: application.MacWindow{
				TitleBar: application.MacTitleBarHidden,
				Backdrop: application.MacBackdropTranslucent,
			},
		}
	}

	// overlayWindowBounds 是好友栏的位置：贴主屏右侧竖直居中，不挡游戏主体。
	overlayWindowBounds := func() (int, int) {
		width, height, ok := primaryScreenSize()
		if !ok {
			return 0, 0
		}
		return clampToZero(width - overlayWindowWidth - overlayMargin),
			clampToZero((height - overlayWindowHeight) / 2)
	}

	// applyFloatingWindowChrome 在窗口句柄就绪后设置原生外观：圆角 +（可选）不抢焦点。
	//
	// 必须等 HWND 就绪 —— 它是在窗口的 run() 里创建的，而 run() 是异步的，
	// 建完立刻取 NativeWindow() 还是空的。
	//
	// 圆角是硬需求：Wails 的无边框窗口是直角矩形，界面里画的是圆角卡片，
	// 不处理的话要么露出直角边框、要么卡片被切成直角（用户看到的
	// 「圆角窗口外面还套了一层直角框」就是这个）。
	applyFloatingWindowChrome := func(window *application.WebviewWindow, nonActivating bool) {
		for attempt := 0; attempt < 60; attempt++ {
			ready := false
			application.InvokeSync(func() {
				handle := window.NativeWindow()
				if handle == nil {
					return
				}
				ready = true
				method, err := winwindow.SetRoundedCorners(handle, floatingCornerRadius)
				if err != nil {
					appLogger.Warning("浮层窗口设置圆角失败：" + err.Error())
				}
				if nonActivating {
					if err := winwindow.MakeNonActivating(handle); err != nil {
						appLogger.Warning("通知浮层无法设置为不抢焦点：" + err.Error())
					}
				}
				applog.LogInfof(
					context.Background(),
					"浮层窗口外观已设置（圆角方式=%s，不抢焦点=%v）",
					method, nonActivating,
				)
			})
			if ready {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		appLogger.Warning("浮层窗口句柄迟迟没有就绪，没能设置窗口外观")
	}

	toggleOverlayWindow := func() {
		floatMu.Lock()
		defer floatMu.Unlock()

		if overlayWindow == nil {
			x, y := overlayWindowBounds()
			// 首次呼出：直接以「可见」建出来（用户就是按了快捷键要看它），
			// 位置建窗时给。刻意**不**聚焦：用户多半正在游戏里，抢焦点会把
			// 游戏踢出前台（全屏游戏会因此最小化）。鼠标点它一样能用，
			// 再按一次快捷键即可收起。
			overlayWindow = wailsApp.Window.NewWithOptions(
				floatingWindowOptions(
					overlayWindowName, "/overlay",
					overlayWindowWidth, overlayWindowHeight,
					x, y, true,
					// 与 FriendsOverlay 卡片的 bg-brand-900 (#0B1020) 同色
					application.NewRGBA(0x0B, 0x10, 0x20, 255),
				),
			)
			// 好友栏要能收键盘输入（按 Esc 收起、进聊天后打字），所以**不**加
			// WS_EX_NOACTIVATE —— 那是通知浮层专用的。
			go applyFloatingWindowChrome(overlayWindow, false)
			return
		}

		if overlayWindow.IsVisible() {
			overlayWindow.Hide()
			return
		}

		x, y := overlayWindowBounds()
		overlayWindow.SetPosition(x, y)
		overlayWindow.Show()
		overlayWindow.Focus()
	}

	hideFriendPlayNotice := func() {
		application.InvokeSync(func() {
			floatMu.Lock()
			defer floatMu.Unlock()
			if noticeWindow != nil {
				noticeWindow.Hide()
			}
		})
	}

	// noticeWindowBounds 是通知浮层的位置：主屏右下角。
	noticeWindowBounds := func() (int, int) {
		width, height, ok := primaryScreenSize()
		if !ok {
			return 0, 0
		}
		return clampToZero(width - noticeWindowWidth - noticeMargin),
			clampToZero(height - noticeWindowHeight - noticeMargin)
	}

	// showFriendPlayNotice 把「好友开始玩游戏」推到屏幕右下角。
	//
	// 返回 false 表示浮层用不了（没有桌面会话 / 建窗失败），调用方会退回系统通知 ——
	// 宁可样式差一点，也不能什么都没提示。
	showFriendPlayNotice := func(event service.FriendPlayEvent) bool {
		// 先把内容存下来：窗口建出来之后浮层前端才挂载，它挂载时会主动拉一次
		// （GetFriendPlayNotice），那才是拿到内容的主路径。事件是给「已经在显示
		// 时又来一条」用的 —— 存的顺序不能晚于建窗，否则第一次通知会是空的。
		stored := overlayService.SetPendingFriendPlayNotice(event)

		shown := false
		application.InvokeSync(func() {
			floatMu.Lock()
			defer floatMu.Unlock()

			x, y := noticeWindowBounds()
			if noticeWindow == nil {
				noticeWindow = wailsApp.Window.NewWithOptions(
					floatingWindowOptions(
						noticeWindowName, "/notice",
						noticeWindowWidth, noticeWindowHeight,
						x, y, true,
						// 与通知卡片底色 #16202d 一致，留白看不出边界
						application.NewRGBA(0x16, 0x20, 0x2D, 255),
					),
				)
				if noticeWindow == nil {
					return
				}
				// 立刻不可见的话不用管它；这里是为了后续每次 Show() 都不抢焦点 ——
				// 通知把正在玩的游戏踢到后台就本末倒置了。
				go applyFloatingWindowChrome(noticeWindow, true)

			} else {
				noticeWindow.SetPosition(x, y)
				noticeWindow.Show()
			}

			noticeWindow.EmitEvent(service.FriendPlayNoticeEvent, stored)
			shown = true
		})
		if shown {
			// 兜底收起：正常由前端倒计时收起，万一前端没跑起来也别让它常驻。
			time.AfterFunc(noticeDismissDelay, hideFriendPlayNotice)
		}
		return shown
	}

	// registerOverlayShortcut 注册（或换绑）好友栏快捷键，返回最终生效的组合。
	//
	// 换绑顺序是「先注册新的、成功后再注销旧的」：反过来的话，用户填了个被别的
	// 程序占用的组合，就会把唯一入口弄没。
	registerOverlayShortcut := func(accelerator string) (string, error) {
		if wailsApp.GlobalShortcut == nil {
			return "", fmt.Errorf("当前环境不支持全局快捷键")
		}
		normalized, err := service.NormalizeOverlayShortcut(accelerator)
		if err != nil {
			return "", err
		}
		if normalized == activeOverlayShortcut && wailsApp.GlobalShortcut.IsRegistered(normalized) {
			return normalized, nil
		}

		if err := wailsApp.GlobalShortcut.Register(normalized, toggleOverlayWindow); err != nil {
			return "", fmt.Errorf("%s 被别的程序占用了，换一个组合试试", service.FormatOverlayShortcut(normalized))
		}

		if activeOverlayShortcut != "" && activeOverlayShortcut != normalized {
			if unregisterErr := wailsApp.GlobalShortcut.Unregister(activeOverlayShortcut); unregisterErr != nil {
				appLogger.Warning("注销旧的好友栏快捷键失败：" + unregisterErr.Error())
			}
		}
		activeOverlayShortcut = normalized
		return normalized, nil
	}

	verifyOverlayShortcut = func() {
		if wailsApp.GlobalShortcut == nil {
			return
		}
		configured := service.DefaultOverlayShortcut
		if normalized, err := service.NormalizeOverlayShortcut(config.OverlayShortcut); err == nil {
			configured = normalized
		}

		// Run() 之前那次注册只是入队，真正绑定发生在启动时，而绑定失败**不会**
		// 从 Register 返回（Wails 只往错误处理器塞一条），所以这里复查一次。
		if wailsApp.GlobalShortcut.IsRegistered(configured) {
			activeOverlayShortcut = configured
			overlayService.SetActiveShortcut(configured)
			appLogger.Info("好友栏快捷键：" + service.FormatOverlayShortcut(configured))
			return
		}

		appLogger.Warning(
			"好友栏快捷键 " + service.FormatOverlayShortcut(configured) + " 没能注册上（多半被别的程序占了），改用备选组合",
		)
		fallback, err := registerOverlayShortcut(service.OverlayShortcutFallback)
		if err != nil {
			activeOverlayShortcut = ""
			overlayService.SetActiveShortcut("")
			appLogger.Warning("备选组合也注册失败，好友栏入口暂时不可用（可在设置里换一个）：" + err.Error())
			return
		}
		overlayService.SetActiveShortcut(fallback)
	}

	// 这里先自己读一次配置：Run() 之前 config 还是 nil（它在 ApplicationStarted
	// 回调里才加载），而快捷键必须在这个时机入队。
	initialShortcut := service.DefaultOverlayShortcut
	if loaded, err := appconf.LoadConfig(); err != nil {
		appLogger.Warning("读取配置失败，好友栏快捷键先按默认值处理：" + err.Error())
	} else if normalized, normalizeErr := service.NormalizeOverlayShortcut(loaded.OverlayShortcut); normalizeErr == nil {
		initialShortcut = normalized
	}

	overlayService.SetOverlayToggler(toggleOverlayWindow)
	overlayService.SetNoticeHider(hideFriendPlayNotice)
	overlayService.SetShortcutApplier(registerOverlayShortcut)
	// 通知改由全局浮层承载：应用内 toast 只在 YukiHub 窗口看得见时才有意义，
	// 而好友开玩的消息大半发生在用户正泡在游戏里的时候。
	accountService.SetFriendPlayNoticePresenter(showFriendPlayNotice)

	if wailsApp.GlobalShortcut != nil {
		activeOverlayShortcut = initialShortcut
		if err := wailsApp.GlobalShortcut.Register(initialShortcut, toggleOverlayWindow); err != nil {
			appLogger.Warning(fmt.Sprintf("登记好友栏快捷键 %s 失败：%v", initialShortcut, err))
		}
	}

	if err := wailsApp.Run(); err != nil {
		appLogger.Fatal(err.Error())
	}
}
