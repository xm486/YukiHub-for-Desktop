package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/common/vo"
	"yukihub/internal/service/exporter"
	"yukihub/internal/service/importer"
	"yukihub/internal/service/yukihubaccount"
	"yukihub/internal/utils/nativenotify"
	"yukihub/internal/wailsruntime"
)

// chatImageMIME 按扩展名给聊天图片的 MIME；不支持时返回空串。
func chatImageMIME(path string) string {
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(path))) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// YukiHub 账号服务相关常量。
const (
	yukihubAccountStatusEvent = "yukihub-account:status-changed"
	yukihubAccountSyncEvent   = "yukihub-account:sync-progress"

	// 心跳间隔与 Android 版一致（服务端 90s 内视为在线、90~300s 视为 away）。
	accountPresenceInterval = 45 * time.Second
	// 手动同步的冷却，同样是 Android 版的 60 秒。
	accountSyncCooldown = 60 * time.Second
)

// AccountService 是 YukiHub 自建账号（yukihub.zh.kg）在桌面端的入口：
// 登录 / 注册 / 资料、云同步游戏库、在线状态，以及好友与聊天。
//
// 登录与云同步之所以放在同一个服务里：它们共用一份访问令牌，
// 而令牌的刷新要集中处理（多处各自刷新会互相顶掉）。
type AccountService struct {
	ctx       context.Context
	db        *sql.DB
	config    *appconf.AppConfig
	runtime   wailsruntime.Runtime
	emitEvent func(string, ...interface{})
	imports   *ImportService
	openURL   func(string) error

	client *yukihubaccount.Client

	mu     sync.Mutex
	syncMu sync.Mutex

	// refreshMu / refreshFlight 保证同一时刻只有一次 refresh 请求在飞。
	// 契约要求（docs/yukihub-api-contract.md 第四节）：并发多个请求同时 401 时
	// refresh 只发一次，其余请求等结果 —— 否则会触发刷新风暴并撞上服务端限速。
	refreshMu     sync.Mutex
	refreshFlight *refreshCall
	// refreshSessionFn 只在测试里替换刷新实现；生产路径恒为 nil。
	refreshSessionFn func() error

	presenceCancel context.CancelFunc
	presenceActive bool
	// 「好友开始玩游戏」轮询（对齐手机版 PresenceService 的 15 秒好友轮询）
	friendPlayCancel context.CancelFunc
	friendPlayActive bool
	// friendListSignature 是上次推给前端的好友列表签名，用于「有变化才推」
	friendListSignature string
	// noticePresenter 由 main 注入：把「好友开始玩游戏」交给全局通知浮层。
	noticePresenter func(FriendPlayEvent) bool
	// 系统通知发送器（惰性创建，见 systemNotifier）
	notifyMu    sync.Mutex
	notifyReady bool
	notifier    *nativenotify.Notifier
	lastSyncAt  time.Time

	now func() time.Time
}

// NewAccountService 创建账号服务。
func NewAccountService() *AccountService {
	service := &AccountService{
		client: yukihubaccount.NewClient(yukihubaccount.DefaultBaseURL),
		now:    time.Now,
	}
	// emitEvent 先给个空实现，别留 nil。
	//
	// 事件通道要到 SetRuntime（窗口建好之后）才注入，而好友轮询在更早的
	// Init 里就起来了 —— 中间那段时间推事件无人可推，**但绝不能是 nil**：
	// nil 函数字段调用会直接 panic，把整个进程带走（实测：带着登录态启动，
	// 第一次好友列表拉成功就闪退，日志停在启动阶段，看不出任何异常）。
	service.emitEvent = func(string, ...interface{}) {}
	return service
}

// Init 注入运行时依赖。imports 用于把云端快照落回本地库（复用现成的导入器）。
//
//wails:ignore
func (s *AccountService) Init(ctx context.Context, db *sql.DB, config *appconf.AppConfig, imports *ImportService) {
	s.ctx = ctx
	s.db = db
	s.config = config
	s.imports = imports
	if s.now == nil {
		s.now = time.Now
	}
	if s.config != nil && s.config.YukiHubAccountAccessToken != "" {
		// 上次是登录状态：应用启动后恢复心跳。
		//
		// 心跳和好友轮询必须一起起 —— 之前只起了心跳，「带着登录态启动应用」
		// 时好友通知轮询压根没跑（只有本次会话里重新登录才会启动），
		// 表现就是「好友都上线了、列表也变了，通知却一直不来」。
		s.startPresence()
		s.startFriendPlayPolling()
	}
}

// SetRuntime 注入 Wails 运行时（用于向前端推事件与打开系统浏览器）。
//
//wails:ignore
func (s *AccountService) SetRuntime(runtime wailsruntime.Runtime) {
	if runtime == nil {
		return
	}
	s.runtime = runtime
	s.openURL = runtime.OpenURL
	s.emitEvent = func(name string, data ...interface{}) {
		runtime.Emit(name, data...)
	}

	// 事件通道现在才算接通：把好友列表的推送签名清掉，让下一次轮询重新推一遍。
	// 不清的话，启动早期那次「无人可推」的推送会被当成已推过，界面要等到
	// 好友状态真发生变化才收到第一份列表。
	s.mu.Lock()
	s.friendListSignature = ""
	s.mu.Unlock()
}

// ==================== 状态 ====================

// GetAccountStatus 返回当前账号状态（不含令牌）。
func (s *AccountService) GetAccountStatus() vo.AccountStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *AccountService) statusLocked() vo.AccountStatus {
	status := vo.AccountStatus{
		ServiceURL:     yukihubaccount.DefaultBaseURL,
		PresenceActive: s.presenceActive,
	}
	if s.config == nil {
		return status
	}
	status.LoggedIn = strings.TrimSpace(s.config.YukiHubAccountAccessToken) != ""
	status.UserID = s.config.YukiHubAccountUserID
	status.UID = s.config.YukiHubAccountUID
	status.Nickname = s.config.YukiHubAccountNickname
	status.Email = s.config.YukiHubAccountEmail
	status.Avatar = s.config.YukiHubAccountAvatar
	status.KungalBound = s.config.YukiHubAccountKungalBound
	status.HikarinagiBound = s.config.YukiHubAccountHikarinagiBound
	status.CloudSyncEnabled = s.config.YukiHubAccountCloudSyncEnabled
	status.SharePlaying = s.config.YukiHubAccountSharePlaying
	// nil = 还没设置过，按默认开启显示（与 friendPlayNotifyEnabled 一致）
	status.FriendPlayNotify = s.config.YukiHubAccountFriendPlayNotify == nil ||
		*s.config.YukiHubAccountFriendPlayNotify
	status.LastSyncAt = s.config.LastYukiHubAccountSyncAt
	status.LastSyncHash = s.config.LastYukiHubAccountSyncHash
	return status
}

func (s *AccountService) emitStatus() {
	if s.emitEvent == nil {
		return
	}
	s.mu.Lock()
	status := s.statusLocked()
	s.mu.Unlock()
	s.emitEvent(yukihubAccountStatusEvent, status)
}

// TestAccountConnection 探测账号服务是否可达（邮箱界面上的「测试连接」）。
func (s *AccountService) TestAccountConnection() error {
	return s.client.Health(s.resolveContext(nil))
}

// ==================== 登录 / 注册 ====================

// SendAccountCode 发送邮箱验证码。purpose 传 "register"（默认）或 "reset"。
func (s *AccountService) SendAccountCode(email string, purpose string) error {
	if strings.TrimSpace(email) == "" {
		return errors.New("请先填写邮箱")
	}
	if strings.TrimSpace(purpose) == "" {
		purpose = yukihubaccount.CodePurposeRegister
	}
	return s.client.SendCode(s.resolveContext(nil), email, purpose)
}

// RegisterAccount 用邮箱 + 验证码注册并直接进入登录状态。
func (s *AccountService) RegisterAccount(email, password, nickname, code string) (vo.AccountStatus, error) {
	if strings.TrimSpace(password) == "" {
		return vo.AccountStatus{}, errors.New("请填写密码")
	}
	session, err := s.client.Register(s.resolveContext(nil), email, password, nickname, code)
	if err != nil {
		return vo.AccountStatus{}, err
	}
	if err := s.applySession(session); err != nil {
		return vo.AccountStatus{}, err
	}
	return s.GetAccountStatus(), nil
}

// LoginAccount 用邮箱 + 密码登录。
func (s *AccountService) LoginAccount(email, password string) (vo.AccountStatus, error) {
	if strings.TrimSpace(email) == "" || strings.TrimSpace(password) == "" {
		return vo.AccountStatus{}, errors.New("请填写邮箱与密码")
	}
	session, err := s.client.Login(s.resolveContext(nil), email, password)
	if err != nil {
		return vo.AccountStatus{}, err
	}
	if err := s.applySession(session); err != nil {
		return vo.AccountStatus{}, err
	}
	return s.GetAccountStatus(), nil
}

// ResetAccountPassword 用邮箱验证码重置密码。
func (s *AccountService) ResetAccountPassword(email, code, password string) error {
	if strings.TrimSpace(password) == "" {
		return errors.New("请填写新密码")
	}
	return s.client.ResetPassword(s.resolveContext(nil), email, code, password)
}

// LogoutAccount 退出登录：清本地会话、停心跳、尽力通知服务端下线。
func (s *AccountService) LogoutAccount() error {
	s.mu.Lock()
	token := ""
	if s.config != nil {
		token = s.config.YukiHubAccountAccessToken
	}
	s.mu.Unlock()

	if token != "" {
		// 下线请求是「尽力而为」：失败也照样清本地，否则用户会卡在退不出去的状态。
		if err := s.client.MarkOffline(s.resolveContext(nil), token); err != nil {
			applog.LogWarningf(s.ctx, "YukiHub 账号：下线通知失败（忽略）：%v", err)
		}
	}
	s.stopPresence()
	s.stopFriendPlayPolling()

	s.mu.Lock()
	if s.config != nil {
		s.config.YukiHubAccountAccessToken = ""
		s.config.YukiHubAccountRefreshToken = ""
		s.config.YukiHubAccountUserID = ""
		s.config.YukiHubAccountUID = 0
		s.config.YukiHubAccountNickname = ""
		s.config.YukiHubAccountAvatar = ""
		s.config.YukiHubAccountKungalBound = false
		s.config.YukiHubAccountHikarinagiBound = false
		s.config.LastYukiHubAccountSyncHash = ""
		s.config.LastYukiHubAccountSyncAt = ""
		s.persistConfigLocked()
	}
	s.mu.Unlock()

	s.emitStatus()
	return nil
}

// applySession 把登录结果写进配置并落盘，同时启动心跳。
func (s *AccountService) applySession(session yukihubaccount.Session) error {
	s.mu.Lock()
	if s.config == nil {
		s.mu.Unlock()
		return errors.New("配置尚未就绪")
	}
	s.config.YukiHubAccountAccessToken = session.AccessToken
	if session.RefreshToken != "" {
		s.config.YukiHubAccountRefreshToken = session.RefreshToken
	}
	if session.User.ID != "" {
		s.config.YukiHubAccountUserID = session.User.ID
	}
	if session.User.UID != 0 {
		s.config.YukiHubAccountUID = session.User.UID
	}
	if session.User.Nickname != "" {
		s.config.YukiHubAccountNickname = session.User.Nickname
	}
	if session.User.Email != "" {
		s.config.YukiHubAccountEmail = session.User.Email
	}
	if session.User.Avatar != "" {
		s.config.YukiHubAccountAvatar = session.User.Avatar
	}
	s.config.YukiHubAccountKungalBound = session.User.KungalBound
	s.config.YukiHubAccountHikarinagiBound = session.User.HikarinagiBound
	// 登录即默认开启「向好友展示正在玩的游戏」。
	//
	// 与手机版一致：手机版读的是 SharedPreferences 的默认值 true
	// （MainActivity：`prefs.getBoolean(KEY_SHARE_PLAYING, true)`），
	// 也就是「没主动关过就是开」。桌面端配置项是普通 bool，没有「未设置」
	// 语义，所以在登录成功这一刻显式置为 true（用户在账号面板里手动关掉后，
	// 同一次登录会话内保持关闭）。
	s.config.YukiHubAccountSharePlaying = true
	s.persistConfigLocked()
	s.mu.Unlock()

	s.startPresence()
	s.startFriendPlayPolling()
	s.emitStatus()
	return nil
}

// ==================== 资料 ====================

// UpdateAccountNickname 修改云端昵称。
func (s *AccountService) UpdateAccountNickname(nickname string) error {
	trimmed := strings.TrimSpace(nickname)
	if trimmed == "" {
		return errors.New("昵称不能为空")
	}
	err := s.withToken(func(token string) error {
		return s.client.UpdateNickname(s.resolveContext(nil), token, trimmed)
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	if s.config != nil {
		s.config.YukiHubAccountNickname = trimmed
		s.persistConfigLocked()
	}
	s.mu.Unlock()
	s.emitStatus()
	return nil
}

// GetAccountLevel 查询等级 / 经验 / 签到状态。
func (s *AccountService) GetAccountLevel() (vo.AccountLevel, error) {
	info, err := accountFetch(s, func(token string) (yukihubaccount.LevelInfo, error) {
		return s.client.GetLevel(s.resolveContext(nil), token)
	})
	if err != nil {
		return vo.AccountLevel{}, err
	}
	return vo.AccountLevel{
		Level:             info.Level,
		Exp:               info.Exp,
		NextLevelTotalExp: info.NextLevelTotalExp,
		IsMaxLevel:        info.IsMaxLevel,
		TodayCheckedIn:    info.TodayCheckedIn,
	}, nil
}

// SetAccountCloudSyncEnabled 开关「登录后自动同步」。
func (s *AccountService) SetAccountCloudSyncEnabled(enabled bool) error {
	s.mu.Lock()
	if s.config != nil {
		s.config.YukiHubAccountCloudSyncEnabled = enabled
		s.persistConfigLocked()
	}
	s.mu.Unlock()
	s.emitStatus()
	return nil
}

// SetAccountSharePlaying 开关「向好友展示正在玩的游戏」。
func (s *AccountService) SetAccountSharePlaying(enabled bool) error {
	s.mu.Lock()
	if s.config != nil {
		s.config.YukiHubAccountSharePlaying = enabled
		s.persistConfigLocked()
	}
	s.mu.Unlock()
	// 立即按新设置发一次心跳，好友那边不用等到下一个周期。
	go func() {
		if err := s.sendHeartbeat(); err != nil {
			applog.LogWarningf(s.ctx, "YukiHub 账号：心跳上报失败（忽略）：%v", err)
		}
	}()
	s.emitStatus()
	return nil
}

// ==================== 云同步 ====================

// SyncAccountNow 立刻与云端同步一次游戏库。
//
// 上传方向复用 YukiHubExporter（产出的就是 Android 侧 schema 5 快照，
// 与手机版 `/sync/upload` 收的格式完全相同）；下载方向复用 YukiHubImporter。
func (s *AccountService) SyncAccountNow() (vo.AccountSyncResult, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	result := vo.AccountSyncResult{}
	if !s.isLoggedIn() {
		return result, yukihubaccount.ErrNotLoggedIn
	}
	// 冷却：与 Android 版一致，1 分钟内只允许一次手动同步。
	if !s.lastSyncAt.IsZero() && s.now().Sub(s.lastSyncAt) < accountSyncCooldown {
		return result, fmt.Errorf("同步冷却中，请稍后再试（%d 秒内仅限一次）",
			int(accountSyncCooldown.Seconds()))
	}

	localSnapshot, err := s.buildLocalSnapshot()
	if err != nil {
		return result, err
	}
	localHash := hashSnapshot(localSnapshot)

	remoteRaw, err := accountFetch(s, func(token string) ([]byte, error) {
		return s.client.DownloadSnapshot(s.resolveContext(nil), token)
	})
	remoteExists := true
	if errors.Is(err, yukihubaccount.ErrCloudSnapshotMissing) {
		remoteExists = false
		err = nil
	}
	if err != nil {
		return result, err
	}

	lastHash := s.lastSyncHash()
	localChanged := lastHash == "" || localHash != lastHash
	remoteChanged := !remoteExists || lastHash == "" || hashSnapshot(remoteRaw) != lastHash

	switch {
	case !remoteExists:
		// 云端还没有数据 → 首次上传
		if err := s.uploadSnapshot(localSnapshot); err != nil {
			return result, err
		}
		result = s.finishSync("uploaded", localSnapshot, vo.AccountSyncResult{})
	case snapshotIsEmpty(localSnapshot) && !snapshotIsEmpty(remoteRaw):
		// 本地游戏库为空、云端有数据 → **一律下载**，绝不用空库覆盖云端。
		//
		// 这条分支覆盖手机版 SyncManager.syncToServer 的「新设备首次同步」分支
		// （本地为空 + 云端有数据 + 从没同步过 → 直接下载），并且**刻意放宽了一点**：
		// 手机版额外要求 lastHash 为空，桌面端不做这个要求。
		//
		// 原因是手机版剩下的分支在「清空游戏库后再点同步」时会走
		// `localChanged && !remoteChanged` → 上传 —— 也就是把云端数据抹成空的。
		// 本同步机制没有任何删除传播（导入是纯增量、没有墓碑），单条游戏的删除
		// 本来就不会同步出去；用一把「同步」按钮顺手清空云端属于纯粹的误伤，
		// 而空库恰恰是重装 / 换设备 / 手滑清库后最需要它的时候。
		// 真要清空云端，应当走显式的覆盖式操作，而不是靠空库上传。
		imported, importErr := s.importSnapshot(remoteRaw)
		if importErr != nil {
			return result, importErr
		}
		result = s.finishSync("downloaded", remoteRaw, vo.AccountSyncResult{Imported: imported})
	case localChanged && !remoteChanged:
		if err := s.uploadSnapshot(localSnapshot); err != nil {
			return result, err
		}
		result = s.finishSync("uploaded", localSnapshot, result)
	case !localChanged && remoteChanged:
		imported, importErr := s.importSnapshot(remoteRaw)
		if importErr != nil {
			return result, importErr
		}
		result = s.finishSync("downloaded", remoteRaw, vo.AccountSyncResult{Imported: imported})
	case localHash == hashSnapshot(remoteRaw):
		// 两边一致，只更新基准哈希
		result = s.finishSync("noop", localSnapshot, result)
		result.Message = "已是最新"
	default:
		// 两边都变了：按 Android 的策略做「云端优先」的合并——
		// 先导入云端，再把合并后的结果上传，避免任何一边被丢掉。
		imported, importErr := s.importSnapshot(remoteRaw)
		if importErr != nil {
			return result, importErr
		}
		merged, buildErr := s.buildLocalSnapshot()
		if buildErr != nil {
			return result, buildErr
		}
		if uploadErr := s.uploadSnapshot(merged); uploadErr != nil {
			return result, uploadErr
		}
		result = s.finishSync("merged", merged, vo.AccountSyncResult{Imported: imported})
	}

	result.SyncedAt = s.now().Format(time.RFC3339)
	s.lastSyncAt = s.now()
	// 同步后游戏库可能变了，通知界面刷新。
	if s.emitEvent != nil {
		s.emitEvent(yukihubAccountSyncEvent, result)
		// 下载 / 合并方向会改动本地库，额外广播一次「同步已落库」，
		// 让游戏库与首页立刻失效缓存重取数据（否则用户会以为同步没生效）。
		if result.Action == "downloaded" || result.Action == "merged" {
			s.emitEvent(selfSyncAppliedEvent, result)
		}
	}
	return result, nil
}

func (s *AccountService) finishSync(action string, snapshot []byte, result vo.AccountSyncResult) vo.AccountSyncResult {
	games, sessions := countSnapshotEntries(snapshot)
	result.Action = action
	result.Games = games
	result.Sessions = sessions

	hash := hashSnapshot(snapshot)
	s.mu.Lock()
	if s.config != nil {
		s.config.LastYukiHubAccountSyncHash = hash
		s.config.LastYukiHubAccountSyncAt = s.now().Format(time.RFC3339)
		s.persistConfigLocked()
	}
	s.mu.Unlock()
	s.emitStatus()
	return result
}

// buildLocalSnapshot 生成 Android 侧 schema 5 快照的 JSON 字节。
func (s *AccountService) buildLocalSnapshot() ([]byte, error) {
	return buildYukiHubSnapshot(s.resolveContext(nil), s.db, s.config)
}

// buildYukiHubSnapshot 生成 Android 侧 schema 5 快照的 JSON 字节。
//
// 账号云同步（传到 yukihub.zh.kg）与自持同步（传到用户自己的 WebDAV）共用这一份
// 构造逻辑：两边产出的必须是**同一个格式**，否则数据在手机端与桌面端之间往返时
// 会互相丢字段。
//
// 快照要带 profile 段（昵称 / 头像），与手机版一致：对端下载后会更新自己的资料。
func buildYukiHubSnapshot(ctx context.Context, db *sql.DB, config *appconf.AppConfig) ([]byte, error) {
	if db == nil {
		return nil, errors.New("数据库尚未就绪")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	exporterInstance := exporter.NewYukiHubExporter(ctx, db)
	if config != nil {
		exporterInstance.SetProfile(config.YukiHubAccountNickname, config.YukiHubAccountAvatar)
		exporterInstance.SetMetadataSource(string(config.CurrentMetadataSource))
	}
	backup, err := exporterInstance.Build()
	if err != nil {
		return nil, fmt.Errorf("生成同步快照失败: %w", err)
	}
	encoded, err := json.Marshal(backup)
	if err != nil {
		return nil, fmt.Errorf("序列化同步快照失败: %w", err)
	}
	return encoded, nil
}

func (s *AccountService) uploadSnapshot(snapshot []byte) error {
	gzipped, err := yukihubaccount.GzipBytes(snapshot)
	if err != nil {
		return err
	}
	return s.withToken(func(token string) error {
		return s.client.UploadSnapshot(s.resolveContext(nil), token, gzipped)
	})
}

// importSnapshot 把云端快照写进本地库，返回成功导入的条目数。
//
// 落盘成临时文件是因为导入器是按路径读取的；导入策略选「同名同路径合并会话」，
// 这样两边都有的游戏不会重复创建，也不会把本地游玩记录冲掉。
func (s *AccountService) importSnapshot(snapshot []byte) (int, error) {
	result, err := importYukiHubSnapshot(s.resolveContext(nil), s.imports, snapshot)
	if err != nil {
		return 0, err
	}
	return result.Success, nil
}

// importYukiHubSnapshot 把快照写进本地库。
//
// 落盘成临时文件是因为导入器是按路径读取的；导入策略选「同名同路径合并会话」，
// 这样两边都有的游戏不会重复创建，也不会把本地游玩记录冲掉。
//
// 账号云同步与自持同步共用这一份实现 —— 导入语义必须完全一致，否则同一份快照
// 走两条通道会得到不同的本地库。
func importYukiHubSnapshot(ctx context.Context, imports *ImportService, snapshot []byte) (importer.ImportResult, error) {
	if imports == nil {
		return importer.ImportResult{}, errors.New("导入服务尚未就绪")
	}
	tempFile, err := os.CreateTemp("", "yukihub-account-sync-*.json")
	if err != nil {
		return importer.ImportResult{}, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if _, err := tempFile.Write(snapshot); err != nil {
		_ = tempFile.Close()
		return importer.ImportResult{}, fmt.Errorf("写入临时快照失败: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return importer.ImportResult{}, fmt.Errorf("写入临时快照失败: %w", err)
	}

	deps := imports.importerDependencies()
	// sync_merge：并集去重地并入对端会话，同时按手机版 importGamesJson 的规则
	// （非空 + 对端 updated_at 不早于本地）更新已有游戏字段。用 merge_sessions
	// 的话，手机端改过的状态/隐藏/NSFW 同步回桌面端会被整段丢掉。
	yukiHubImporter := importer.NewYukiHubImporter(deps)
	result, err := yukiHubImporter.Import(tempPath, false, importer.SamePathActionSyncMerge)
	if err != nil {
		return importer.ImportResult{}, fmt.Errorf("导入快照失败: %w", err)
	}
	// 快照里的 settings.metadata_source 属于跨端全局偏好，手机版导入时会落回
	// 本地设置；桌面端同样采纳，否则「手机端改了资料源」会被桌面端下次上传顶回去。
	imports.applyImportedMetadataSource(yukiHubImporter.MetadataSource())
	applog.LogInfof(ctx, "YukiHub 同步：快照导入完成 success=%d skipped=%d failed=%d sessions=%d",
		result.Success, result.Skipped, result.Failed, result.SessionsImported)
	return result, nil
}

// ==================== 在线状态 ====================

func (s *AccountService) startPresence() {
	s.mu.Lock()
	if s.presenceCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.resolveContext(nil))
	s.presenceCancel = cancel
	s.presenceActive = true
	s.mu.Unlock()

	go func() {
		// 先立刻发一次，别让好友等到第一个周期才看到你上线。
		if err := s.sendHeartbeat(); err != nil {
			applog.LogWarningf(s.ctx, "YukiHub 账号：心跳上报失败（忽略）：%v", err)
		}
		ticker := time.NewTicker(accountPresenceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.sendHeartbeat(); err != nil {
					applog.LogWarningf(s.ctx, "YukiHub 账号：心跳上报失败（忽略）：%v", err)
				}
			}
		}
	}()
}

func (s *AccountService) stopPresence() {
	s.mu.Lock()
	cancel := s.presenceCancel
	s.presenceCancel = nil
	s.presenceActive = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *AccountService) sendHeartbeat() error {
	if !s.isLoggedIn() {
		return nil
	}
	activity := ""
	s.mu.Lock()
	sharePlaying := s.config != nil && s.config.YukiHubAccountSharePlaying
	s.mu.Unlock()
	if sharePlaying {
		activity = s.resolvePlayingActivity()
	}
	return s.withToken(func(token string) error {
		return s.client.Heartbeat(s.resolveContext(nil), token, yukihubaccount.PresenceOnline, activity)
	})
}

// NotifyOffline 尽力上报一次下线（**关闭客户端**时调用，退出登录走 LogoutAccount）。
//
// 契约（docs/yukihub-presence-platform.md §3.3）要求「退出登录 / 关闭客户端时
// 尽力调用一次」：调不通也没关系，服务端 10 分钟收不到心跳会自动判离线。
// 失败只记日志，绝不阻塞退出流程；调用方负责限时。
//
// 会**先停掉心跳循环**再上报：否则一个刚好到点的 tick 会把状态又顶回在线，
// 好友那边要再过 10 分钟才看到你离开。
func (s *AccountService) NotifyOffline(ctx context.Context) error {
	if !s.isLoggedIn() {
		return nil
	}
	s.stopPresence()

	s.mu.Lock()
	token := ""
	if s.config != nil {
		token = strings.TrimSpace(s.config.YukiHubAccountAccessToken)
	}
	s.mu.Unlock()
	if token == "" {
		return nil
	}
	return s.client.MarkOffline(s.resolveContext(ctx), token)
}

// resolvePlayingActivity 返回「正在玩：xxx」；没有正在进行的游玩时返回空串。
func (s *AccountService) resolvePlayingActivity() string {
	if s.db == nil {
		return ""
	}
	var title string
	// 未结束的游玩记录（end_time 为空）就是当前在玩的游戏。
	err := s.db.QueryRowContext(s.resolveContext(nil), `
		SELECT g.name FROM play_sessions ps
		JOIN games g ON g.id = ps.game_id
		WHERE ps.end_time IS NULL
		ORDER BY ps.start_time DESC LIMIT 1`).Scan(&title)
	if err != nil || strings.TrimSpace(title) == "" {
		return ""
	}
	return playingActivityPrefix + title
}

// ==================== 社交（好友 / 私聊 / 群聊） ====================

// ListFriends 好友列表与待处理申请数。
func (s *AccountService) ListFriends() (yukihubaccount.FriendList, error) {
	return accountFetch(s, func(token string) (yukihubaccount.FriendList, error) {
		return s.client.ListFriends(s.resolveContext(nil), token)
	})
}

// ListFriendRequests 好友申请列表（收到的 + 已发出的）。
//
// 与 ListFriends 分开：列表接口里的 `pendingRequests` 只是个**数字**，
// 申请内容要单独拉 `/friends/requests`（手机版 SocialApiClient.getFriendRequests）。
func (s *AccountService) ListFriendRequests() (yukihubaccount.FriendRequests, error) {
	return accountFetch(s, func(token string) (yukihubaccount.FriendRequests, error) {
		return s.client.ListFriendRequests(s.resolveContext(nil), token)
	})
}

// SearchUsers 搜用户。
func (s *AccountService) SearchUsers(keyword string) ([]yukihubaccount.Friend, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, errors.New("请输入要搜索的昵称或 UID")
	}
	return accountFetch(s, func(token string) ([]yukihubaccount.Friend, error) {
		return s.client.SearchUsers(s.resolveContext(nil), token, keyword)
	})
}

// SendFriendRequest 发好友申请。
func (s *AccountService) SendFriendRequest(target string) error {
	return s.withToken(func(token string) error {
		return s.client.SendFriendRequest(s.resolveContext(nil), token, target)
	})
}

// AcceptFriendRequest 接受好友申请。
//
// friendshipID 是**数字**（服务端下发与回传都是数字）；只拿得到对方 uid 的场景
// （资料页）传 0 + uid。
func (s *AccountService) AcceptFriendRequest(friendshipID int64, uid int64) error {
	return s.withToken(func(token string) error {
		return s.client.AcceptFriendRequest(s.resolveContext(nil), token, friendshipID, uid)
	})
}

// RejectFriendRequest 拒绝好友申请。
func (s *AccountService) RejectFriendRequest(friendshipID int64) error {
	return s.withToken(func(token string) error {
		return s.client.RejectFriendRequest(s.resolveContext(nil), token, friendshipID)
	})
}

// RemoveFriend 删除好友。
func (s *AccountService) RemoveFriend(friendID string) error {
	return s.withToken(func(token string) error {
		return s.client.RemoveFriend(s.resolveContext(nil), token, friendID)
	})
}

// SetFriendNote 设置好友备注。
func (s *AccountService) SetFriendNote(friendID, note string) error {
	return s.withToken(func(token string) error {
		return s.client.SetFriendNote(s.resolveContext(nil), token, friendID, note)
	})
}

// SendChatMessage 发私聊消息。
func (s *AccountService) SendChatMessage(receiverID, content, msgType, replyToID string) (yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) (yukihubaccount.ChatMessage, error) {
		return s.client.SendChatMessage(s.resolveContext(nil), token, receiverID, content, msgType, replyToID)
	})
}

// GetChatHistory 拉与某人的历史消息。
func (s *AccountService) GetChatHistory(friendID string, offset, limit int) ([]yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatMessage, error) {
		return s.client.ChatHistory(s.resolveContext(nil), token, friendID, offset, limit)
	})
}

// PollChatMessages 拉新消息（前端每 10 秒调一次，与手机版一致）。
func (s *AccountService) PollChatMessages(afterID, friendID string, peek bool) ([]yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatMessage, error) {
		return s.client.ChatPoll(s.resolveContext(nil), token, afterID, friendID, peek)
	})
}

// GetChatUnreadCount 未读消息总数。
func (s *AccountService) GetChatUnreadCount() (int, error) {
	return accountFetch(s, func(token string) (int, error) {
		return s.client.UnreadTotal(s.resolveContext(nil), token)
	})
}

// ListChatEmojis 本站表情列表。
func (s *AccountService) ListChatEmojis() ([]yukihubaccount.ChatEmoji, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatEmoji, error) {
		return s.client.ListEmojis(s.resolveContext(nil), token)
	})
}

// SelectChatImage 打开图片选择对话框，选中后读取文件内容（供聊天图片消息用）。
//
// 返回 (路径, 内容字节, MIME 类型)；用户取消时路径为空串、其余为零值。
// 压缩在浏览器侧做不了、后端再做一遍太绕：服务端上限 500KB，这里只校验大小
// 并按扩展名给出 MIME，超限时直接报错提示用户换图（与手机版行为一致）。
func (s *AccountService) SelectChatImage() (vo.ChatImagePick, error) {
	result := vo.ChatImagePick{}
	if s.runtime == nil {
		return result, errors.New("窗口尚未就绪")
	}
	path, err := s.runtime.OpenFile(wailsruntime.OpenDialogOptions{
		Title: "选择要发送的图片",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "图片 (jpg/png/webp)", Pattern: "*.jpg;*.jpeg;*.png;*.webp"},
		},
	})
	if err != nil {
		return result, fmt.Errorf("打开图片选择失败: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		// 用户取消，不算错误
		return result, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return result, fmt.Errorf("读取图片失败: %w", err)
	}
	// 服务端上限 500KB（与手机版 ChatImagePicker.MAX_BYTES 的口径一致，
	// 留 20KB 余量），超出直接拒绝而不是静默压缩——压缩交给用户自己裁剪。
	const maxImageBytes = 500 * 1024
	if len(data) > maxImageBytes {
		return result, fmt.Errorf("图片超过 500KB 上限，请先压缩或裁剪后再发送")
	}

	mimeType := chatImageMIME(path)
	if mimeType == "" {
		return result, errors.New("仅支持 jpg / png / webp 图片")
	}
	result.Path = path
	result.Data = data
	result.MimeType = mimeType
	return result, nil
}

// UploadChatImage 上传聊天图片字节，返回可直接放进消息 content 的 URL。
func (s *AccountService) UploadChatImage(data []byte, mimeType string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("图片内容为空")
	}
	return accountFetch(s, func(token string) (string, error) {
		return s.client.UploadChatImage(s.resolveContext(nil), token, mimeType, data)
	})
}

// ListChatStickerPacks 拉未萌贴纸包列表（服务端代理）。
// Enabled=false 表示服务端未启用，界面隐藏「未萌贴纸」入口。
func (s *AccountService) ListChatStickerPacks() (vo.ChatStickerList, error) {
	return accountFetch(s, func(token string) (vo.ChatStickerList, error) {
		enabled, rawPacks, err := s.client.ListStickerPacks(s.resolveContext(nil), token)
		if err != nil {
			return vo.ChatStickerList{}, err
		}
		list := vo.ChatStickerList{Enabled: enabled, Packs: make([]vo.ChatStickerPack, 0, len(rawPacks))}
		for _, raw := range rawPacks {
			pack := vo.ChatStickerPack{
				ID:           pickServiceString(raw, "id"),
				Title:        pickServiceString(raw, "title"),
				Cover:        pickServiceString(raw, "cover"),
				StickerCount: int(pickServiceInt64(raw, "sticker_count", "stickerCount")),
			}
			if pack.ID != "" && pack.Cover != "" {
				list.Packs = append(list.Packs, pack)
			}
		}
		return list, nil
	})
}

// ListChatStickerURLs 拉某个未萌贴纸包里的全部表情地址。
func (s *AccountService) ListChatStickerURLs(packID string) ([]string, error) {
	if strings.TrimSpace(packID) == "" {
		return nil, errors.New("缺少贴纸包 id")
	}
	return accountFetch(s, func(token string) ([]string, error) {
		return s.client.ListStickerURLs(s.resolveContext(nil), token, packID)
	})
}

// pickServiceString / pickServiceInt64 是 account_service 里的小工具：
// 服务端贴纸响应的字段名在 snake/camel 之间摇摆，逐个候选取值。
func pickServiceString(source map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := source[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func pickServiceInt64(source map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := source[key].(float64); ok {
			return int64(value)
		}
	}
	return 0
}

// ListChatGroups 群列表。
func (s *AccountService) ListChatGroups() ([]yukihubaccount.ChatGroup, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatGroup, error) {
		return s.client.ListGroups(s.resolveContext(nil), token)
	})
}

// GetGroupHistory 群历史消息。
func (s *AccountService) GetGroupHistory(groupID string, offset, limit int) ([]yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatMessage, error) {
		history, _, historyErr := s.client.GroupHistory(s.resolveContext(nil), token, groupID, offset, limit)
		return history, historyErr
	})
}

// ManageGroupMessage 撤回或删除群消息（仅管理员，服务端会校验权限）。
//
// action 取 "recall"（撤回）或 "delete"（删除），与手机版 doManageGroupMessage 一致。
func (s *AccountService) ManageGroupMessage(messageID, action string) error {
	normalized := yukihubaccount.GroupMessageAction(strings.TrimSpace(action))
	if normalized != yukihubaccount.GroupActionRecall && normalized != yukihubaccount.GroupActionDelete {
		return fmt.Errorf("不支持的群消息操作: %s", action)
	}
	return s.withToken(func(token string) error {
		return s.client.ManageGroupMessage(s.resolveContext(nil), token, messageID, normalized)
	})
}

// ReportChatMessage 举报一条聊天消息。
//
// scene = "chat"（私聊）/ "group"（群聊），群聊时 groupID 必填。
func (s *AccountService) ReportChatMessage(scene, messageID, groupID, reason string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("消息 ID 为空")
	}
	return s.withToken(func(token string) error {
		return s.client.ReportChatMessage(s.resolveContext(nil), token, scene, messageID, groupID, reason)
	})
}

// GetUserProfile 拉取某个 UID 的用户资料（友链列表、群聊点头像进资料页）。
func (s *AccountService) GetUserProfile(uid int64) (yukihubaccount.UserProfile, error) {
	if uid <= 0 {
		return yukihubaccount.UserProfile{}, errors.New("UID 无效")
	}
	return accountFetch(s, func(token string) (yukihubaccount.UserProfile, error) {
		return s.client.UserProfile(s.resolveContext(nil), token, uid)
	})
}

// GetGroupOnlineCount 群聊当前在线人数。
//
// 服务端把它挂在群历史接口的响应里（与手机版 getGroupMessages 的 onlineCount
// 同一来源），所以这里拉 1 条消息顺带取人数——标题栏「🟢N在线」用。
func (s *AccountService) GetGroupOnlineCount(groupID string) (int, error) {
	return accountFetch(s, func(token string) (int, error) {
		_, count, err := s.client.GroupHistory(s.resolveContext(nil), token, groupID, 0, 1)
		return count, err
	})
}

// SendGroupMessage 发群消息。
func (s *AccountService) SendGroupMessage(groupID, content, msgType, replyToID string) (yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) (yukihubaccount.ChatMessage, error) {
		return s.client.SendGroupMessage(s.resolveContext(nil), token, groupID, content, msgType, replyToID)
	})
}

// PollGroupMessages 拉群新消息。
func (s *AccountService) PollGroupMessages(groupID, afterID string) ([]yukihubaccount.ChatMessage, error) {
	return accountFetch(s, func(token string) ([]yukihubaccount.ChatMessage, error) {
		items, _, pollErr := s.client.PollGroup(s.resolveContext(nil), token, groupID, afterID)
		return items, pollErr
	})
}

// ==================== 令牌与工具 ====================

func (s *AccountService) isLoggedIn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config != nil && strings.TrimSpace(s.config.YukiHubAccountAccessToken) != ""
}

// withToken 带令牌执行；遇到 401 就刷新一次再重试。
func (s *AccountService) withToken(fn func(token string) error) error {
	token, err := s.requireToken()
	if err != nil {
		return err
	}
	err = fn(token)
	if err == nil || !errors.Is(err, yukihubaccount.ErrUnauthorized) {
		return err
	}
	if refreshErr := s.refreshSession(); refreshErr != nil {
		s.markSessionExpired()
		return err
	}
	token, err = s.requireToken()
	if err != nil {
		return err
	}
	return fn(token)
}

// accountFetch 与 withToken 相同，但需要返回值。
//
// Go 的方法不能自带类型参数，所以这是个包级泛型函数，第一个参数显式传服务实例。
func accountFetch[T any](s *AccountService, fn func(token string) (T, error)) (T, error) {
	var zero T
	token, err := s.requireToken()
	if err != nil {
		return zero, err
	}
	result, err := fn(token)
	if err == nil || !errors.Is(err, yukihubaccount.ErrUnauthorized) {
		return result, err
	}
	// 401 后只重试**一次**（契约第四节），不循环。
	//
	// 刷新失败时的处理要分清两种情况（契约第四节第 4 条）：
	//   - 令牌确实失效（refresh 返回 401）或账号被禁用（403）→ 清空本地会话；
	//   - 网络异常 / 超时 / 服务端 5xx → **保留登录态**，只把错误抛给界面。
	// 之前一律 markSessionExpired，断网时会把用户直接登出。
	if refreshErr := s.refreshSessionShared(); refreshErr != nil {
		if isSessionInvalidError(refreshErr) {
			s.markSessionExpired()
		} else {
			applog.LogWarningf(s.ctx, "YukiHub 账号：刷新令牌失败（保留登录态）：%v", refreshErr)
		}
		return zero, err
	}
	token, err = s.requireToken()
	if err != nil {
		return zero, err
	}
	// 重试仍失败时不再清会话：新令牌刚换出来就被拒多半是服务端抖动，
	// 清掉反而会让用户莫名其妙掉线。
	return fn(token)
}

// isSessionInvalidError 判断刷新失败是否意味着「这个会话已经彻底没救了」。
func isSessionInvalidError(err error) bool {
	return errors.Is(err, yukihubaccount.ErrUnauthorized) ||
		errors.Is(err, yukihubaccount.ErrAccountDisabled)
}

// refreshCall 是一次进行中的令牌刷新。
type refreshCall struct {
	done chan struct{}
	err  error
}

// refreshSessionShared 保证同一时刻只有一次 refresh 请求在飞。
//
// 并发请求同时 401 时，后来者等待第一次的结果复用，而不是各自去刷 —— 否则会
// 打出刷新风暴，服务端开始限速（429），客户端反而被锁在登出状态。
func (s *AccountService) refreshSessionShared() error {
	s.refreshMu.Lock()
	if call := s.refreshFlight; call != nil {
		s.refreshMu.Unlock()
		<-call.done
		return call.err
	}
	call := &refreshCall{done: make(chan struct{})}
	s.refreshFlight = call
	s.refreshMu.Unlock()

	// 无论成功、失败还是 panic 都要唤醒等待者，否则会永久阻塞。
	defer func() {
		s.refreshMu.Lock()
		s.refreshFlight = nil
		s.refreshMu.Unlock()
		close(call.done)
	}()

	call.err = s.refreshSession()
	return call.err
}

func (s *AccountService) requireToken() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil || strings.TrimSpace(s.config.YukiHubAccountAccessToken) == "" {
		return "", yukihubaccount.ErrNotLoggedIn
	}
	return s.config.YukiHubAccountAccessToken, nil
}

// refreshSession 用 refresh token 换新令牌。没有 refresh token 就直接失败。
func (s *AccountService) refreshSession() error {
	// 测试注入点：让并发刷新测试不必真的走网络与配置落盘。
	if s.refreshSessionFn != nil {
		return s.refreshSessionFn()
	}
	s.mu.Lock()
	refreshToken := ""
	if s.config != nil {
		refreshToken = strings.TrimSpace(s.config.YukiHubAccountRefreshToken)
	}
	s.mu.Unlock()

	if refreshToken == "" {
		return errors.New("没有可用的刷新令牌，请重新登录")
	}
	session, err := s.client.Refresh(s.resolveContext(nil), refreshToken)
	if err != nil {
		return err
	}
	return s.applySession(session)
}

// markSessionExpired 刷新也失败时清掉会话，界面据此提示重新登录。
func (s *AccountService) markSessionExpired() {
	s.stopPresence()
	s.stopFriendPlayPolling()
	s.mu.Lock()
	if s.config != nil {
		s.config.YukiHubAccountAccessToken = ""
		s.config.YukiHubAccountRefreshToken = ""
		s.persistConfigLocked()
	}
	s.mu.Unlock()
	s.emitStatus()
}

// persistConfigLocked 落盘配置（调用方需持有 s.mu）。
func (s *AccountService) persistConfigLocked() {
	if s.config == nil {
		return
	}
	if err := appconf.SaveConfig(s.config); err != nil {
		applog.LogErrorf(s.ctx, "YukiHub 账号：保存配置失败: %v", err)
	}
}

func (s *AccountService) lastSyncHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.config == nil {
		return ""
	}
	return strings.TrimSpace(s.config.LastYukiHubAccountSyncHash)
}

func (s *AccountService) resolveContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// hashSnapshot 计算快照哈希（与 Android 版一样对未压缩 JSON 求 SHA-256）。
func hashSnapshot(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// countSnapshotEntries 从快照 JSON 里数出游戏数与游玩记录数（仅用于界面提示）。
func countSnapshotEntries(data []byte) (int, int) {
	var parsed struct {
		Games        []json.RawMessage `json:"games"`
		PlaySessions []json.RawMessage `json:"play_sessions"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return 0, 0
	}
	return len(parsed.Games), len(parsed.PlaySessions)
}

// snapshotIsEmpty 判断快照是否「没有游戏库」，对应手机版 SyncManager.isSnapshotEmpty：
// 只看 games 数组为不为空，不看游玩记录 / 元数据缓存。
func snapshotIsEmpty(data []byte) bool {
	games, _ := countSnapshotEntries(data)
	return games == 0
}
