package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"yukihub/internal/appconf"
	"yukihub/internal/applog"
	"yukihub/internal/common/vo"
	"yukihub/internal/service/gamehelper"
	"yukihub/internal/utils/httputils"
	"yukihub/internal/utils/metadata"
	"yukihub/internal/version"
	"yukihub/internal/wailsruntime"
)

// NextMoe（未萌）用户令牌 OAuth 管线：授权码 + PKCE S256，public client，无 client_secret。
//
// 端点与契约（developer.nextmoe.dev）：
//   - 授权：GET https://account.nextmoe.com/api/v1/oauth/authorize，必须用系统浏览器打开
//   - 回调：http://127.0.0.1:{port}/callback，端口无关匹配，本地回环收码
//   - 换码/刷新：POST /api/v1/oauth/token，不带 secret，带 code_verifier / refresh_token
//   - refresh 每次轮换：旧凭据立即失效，拿到新的必须原地覆盖
const (
	nextMoeOAuthAuthorizeURL = "https://account.nextmoe.com/api/v1/oauth/authorize"
	nextMoeOAuthTokenURL     = "https://account.nextmoe.com/api/v1/oauth/token"
	// nextMoeOAuthClientID 是公开标识（非机密），复用现役「YukiHub Android」客户端。
	nextMoeOAuthClientID = "16cc006913d6b666c6b1a1a115f644de"
	nextMoeOAuthScopes   = "openid profile catalog:read"

	nextMoeOAuthCallbackPort = 14792
	nextMoeOAuthCallbackPath = "/callback"

	nextMoeAuthTimeout      = 5 * time.Minute
	nextMoeTokenRefreshSkew = 1 * time.Minute
	nextMoeHTTPTimeout      = 30 * time.Second
	nextMoeAuthEventName    = "nextmoe:auth-status-changed"
)

type nextMoeAuthSession struct {
	resultChan   chan nextMoeAuthResult
	server       *http.Server
	listener     net.Listener
	state        string
	codeVerifier string
	redirectURI  string
}

type nextMoeAuthResult struct {
	Code  string
	Error string
}

type nextMoeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

// NextMoeService 负责 NextMoe 授权链路与用户令牌的存取刷新。
type NextMoeService struct {
	ctx        context.Context
	config     *appconf.AppConfig
	httpClient *http.Client
	runtime    wailsruntime.Runtime
	openURL    func(string) error
	emitEvent  func(string, ...interface{})
	now        func() time.Time
	mu         sync.Mutex
}

func NewNextMoeService() *NextMoeService {
	runtime := wailsruntime.Unavailable()
	return &NextMoeService{
		runtime:   runtime,
		openURL:   runtime.OpenURL,
		emitEvent: func(name string, data ...interface{}) { runtime.Emit(name, data...) },
		now:       time.Now,
	}
}

//wails:ignore
func (s *NextMoeService) Init(ctx context.Context, config *appconf.AppConfig) {
	s.ctx = ctx
	s.config = config
	if s.httpClient == nil {
		client, _, err := httputils.NewClient(httputils.ClientOptions{
			Timeout:     nextMoeHTTPTimeout,
			ProxyConfig: config,
		})
		if err != nil {
			applog.LogWarningf(ctx, "failed to create NextMoe HTTP client with proxy config: %v", err)
			client = &http.Client{Timeout: nextMoeHTTPTimeout}
		}
		s.httpClient = client
	}
	if s.now == nil {
		s.now = time.Now
	}
}

//wails:ignore
func (s *NextMoeService) SetRuntime(runtime wailsruntime.Runtime) {
	if runtime == nil {
		return
	}
	s.runtime = runtime
	s.openURL = runtime.OpenURL
	s.emitEvent = func(name string, data ...interface{}) {
		runtime.Emit(name, data...)
	}
}

//wails:ignore
func (s *NextMoeService) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.httpClient = client
	}
}

//wails:ignore
func (s *NextMoeService) SetOpenURLFunc(openURL func(string) error) {
	if openURL != nil {
		s.openURL = openURL
	}
}

//wails:ignore
func (s *NextMoeService) SetNowFunc(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

//wails:ignore
func (s *NextMoeService) SetEventEmitter(emit func(string, ...interface{})) {
	s.emitEvent = emit
}

func (s *NextMoeService) GetAuthStatus() (vo.NextMoeAuthStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildAuthStatusLocked(), nil
}

func (s *NextMoeService) GetProfile() (vo.NextMoeProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.config == nil {
		return vo.NextMoeProfile{}, fmt.Errorf("NextMoe 配置未初始化")
	}
	if !s.buildAuthStatusLocked().Authorized {
		return vo.NextMoeProfile{}, fmt.Errorf("NextMoe 未授权")
	}
	return vo.NextMoeProfile{AccountLabel: strings.TrimSpace(s.config.NextMoeAccountLabel)}, nil
}

func (s *NextMoeService) StartAuth() (vo.NextMoeAuthStatus, error) {
	session, err := newNextMoeAuthSession()
	if err != nil {
		return vo.NextMoeAuthStatus{}, err
	}
	defer session.shutdown()

	authURL := buildNextMoeAuthURL(session)
	if err := s.openURL(authURL); err != nil {
		return vo.NextMoeAuthStatus{}, fmt.Errorf("打开 NextMoe 授权页面失败: %w", err)
	}

	timer := time.NewTimer(nextMoeAuthTimeout)
	defer timer.Stop()

	select {
	case result := <-session.resultChan:
		if result.Error != "" {
			return vo.NextMoeAuthStatus{}, fmt.Errorf("NextMoe 授权失败: %s", result.Error)
		}

		tokenResp, err := s.exchangeAuthorizationCode(s.ctx, result.Code, session.redirectURI, session.codeVerifier)
		if err != nil {
			return vo.NextMoeAuthStatus{}, err
		}

		s.mu.Lock()
		status, persistErr := s.persistAuthorizedStateLocked(tokenResp)
		s.mu.Unlock()
		if persistErr != nil {
			return vo.NextMoeAuthStatus{}, persistErr
		}

		s.emitAuthStatusChanged(status)
		applog.LogInfof(s.ctx, "NextMoe OAuth authorized")
		return status, nil
	case <-timer.C:
		return vo.NextMoeAuthStatus{}, fmt.Errorf("NextMoe 授权超时")
	case <-s.resolveContext(s.ctx).Done():
		return vo.NextMoeAuthStatus{}, s.resolveContext(s.ctx).Err()
	}
}

func (s *NextMoeService) Disconnect() (vo.NextMoeAuthStatus, error) {
	s.mu.Lock()
	status, err := s.clearAuthorizationLocked()
	s.mu.Unlock()
	if err != nil {
		return vo.NextMoeAuthStatus{}, err
	}

	s.emitAuthStatusChanged(status)
	applog.LogInfof(s.ctx, "NextMoe OAuth disconnected locally")
	return status, nil
}

func (s *NextMoeService) fetchMetadataByID(ctx context.Context, sourceID string) (metadata.MetadataResult, error) {
	getter := s.newMetadataGetter()
	token := s.resolveAccessToken(ctx)

	result, err := getter.FetchMetadata(sourceID, token)
	if err == nil || !metadata.IsNextMoeUnauthorizedError(err) || token == "" {
		return result, err
	}
	return retryNextMoeWithRefreshedToken(s, ctx, func(retryToken string) (metadata.MetadataResult, error) {
		return getter.FetchMetadata(sourceID, retryToken)
	})
}

func (s *NextMoeService) fetchMetadataByName(ctx context.Context, name string) (metadata.MetadataResult, error) {
	results, err := s.fetchMetadataCandidatesByName(ctx, name)
	if err != nil {
		return metadata.MetadataResult{}, err
	}
	return results[0], nil
}

func (s *NextMoeService) fetchMetadataCandidatesByName(ctx context.Context, name string) ([]metadata.MetadataResult, error) {
	getter := s.newMetadataGetter()
	token := s.resolveAccessToken(ctx)

	result, err := getter.FetchMetadataCandidatesByName(name, token)
	if err == nil || !metadata.IsNextMoeUnauthorizedError(err) || token == "" {
		return result, err
	}
	return retryNextMoeWithRefreshedToken(s, ctx, func(retryToken string) ([]metadata.MetadataResult, error) {
		return getter.FetchMetadataCandidatesByName(name, retryToken)
	})
}

// retryNextMoeWithRefreshedToken 在「带着令牌仍被拒（401）」时刷新一次令牌并重放。
//
// 刷新失败**不**把整次抓取判死：账号处于登录态但令牌不可用时退回匿名档重放
// （目录只读面对两种身份返回逐字节相同），并留一条 warning ——
// 与手机版 NextMoeClient.apiGetOnce 的降级语义一致。
// 关键点是**不能静默**：否则用户会「以为在用账号配额、其实按 IP 被限流」，
// 那正是「登录了额度还是一样」的观感来源。
//
// 写成包级泛型函数而不是方法，是因为 Go 不允许方法带类型参数。
func retryNextMoeWithRefreshedToken[T any](
	s *NextMoeService,
	ctx context.Context,
	replay func(token string) (T, error),
) (T, error) {
	var zero T

	refreshedToken, refreshErr := s.refreshAccessToken(ctx)
	if refreshErr != nil || strings.TrimSpace(refreshedToken) == "" {
		applog.LogWarningf(s.ctx, "NextMoe access token refresh failed, replaying with the anonymous quota: %v", refreshErr)
		return replay("")
	}

	result, err := replay(refreshedToken)
	if err == nil {
		return result, nil
	}
	if metadata.IsNextMoeUnauthorizedError(err) {
		// 刷新过还是 401：令牌已被服务端撤销，必须让用户去重新授权，
		// 不能无声退化成匿名档。
		return zero, fmt.Errorf("NextMoe 登录已失效，请在「设置 → 数据来源」重新登录 NextMoe 账号：%w", err)
	}
	return result, err
}

// resolveAccessToken 返回本次请求要带的访问令牌；空串表示按匿名档发送。
//
// 与手机版 NextMoeClient.apiGetOnce 的「登录可选」语义一致：目录只读面对匿名与
// 登录身份返回逐字节相同的结果，所以未登录一样能用本源（只是配额按出口 IP 计）。
// 账号处于登录态却拿不到可用令牌（刷新失败 / 瞬态故障）时，退化为匿名并留痕。
func (s *NextMoeService) resolveAccessToken(ctx context.Context) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.config == nil {
		return ""
	}
	accessToken := strings.TrimSpace(s.config.NextMoeAccessToken)
	if accessToken != "" && !s.shouldRefreshTokenLocked() {
		return accessToken
	}
	if strings.TrimSpace(s.config.NextMoeRefreshToken) == "" {
		return ""
	}

	token, err := s.refreshAccessTokenLocked(ctx)
	if err != nil {
		applog.LogWarningf(s.ctx, "NextMoe access token unavailable, request will use the anonymous quota: %v", err)
		return ""
	}
	return token
}

func (s *NextMoeService) newMetadataGetter() *metadata.NextMoeInfoGetter {
	return metadata.NewNextMoeInfoGetter(gamehelper.MetadataGetterOptions(s.config)...)
}

func (s *NextMoeService) refreshAccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshAccessTokenLocked(ctx)
}

func (s *NextMoeService) shouldRefreshTokenLocked() bool {
	if s.config == nil {
		return false
	}
	expiresAtRaw := strings.TrimSpace(s.config.NextMoeTokenExpiresAt)
	if expiresAtRaw == "" {
		return strings.TrimSpace(s.config.NextMoeRefreshToken) != ""
	}
	expiresAt, err := time.Parse(time.RFC3339, expiresAtRaw)
	if err != nil {
		return strings.TrimSpace(s.config.NextMoeRefreshToken) != ""
	}
	return !s.now().Add(nextMoeTokenRefreshSkew).Before(expiresAt)
}

func (s *NextMoeService) refreshAccessTokenLocked(ctx context.Context) (string, error) {
	if s.config == nil {
		return "", fmt.Errorf("NextMoe 配置未初始化")
	}
	refreshToken := strings.TrimSpace(s.config.NextMoeRefreshToken)
	if refreshToken == "" {
		return "", fmt.Errorf("NextMoe 未授权")
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {nextMoeOAuthClientID},
		"refresh_token": {refreshToken},
	}
	tokenResp, statusCode, body, err := s.requestToken(ctx, form)
	if err != nil {
		return "", fmt.Errorf("刷新 NextMoe access token 失败: %w", err)
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError || tokenResp.Error != "" {
		message := firstNonEmptyString(tokenResp.ErrorDesc, tokenResp.Error, "NextMoe 授权已失效，请重新授权")
		status, clearErr := s.clearAuthorizationLocked()
		if clearErr == nil {
			s.emitAuthStatusChanged(status)
		}
		return "", fmt.Errorf("NextMoe refresh token 无效: %s", message)
	}
	// 5xx 属服务端瞬态故障：保留本地凭据，等下次再试。
	if statusCode >= http.StatusInternalServerError {
		return "", fmt.Errorf("NextMoe refresh 请求失败，HTTP %d: %s", statusCode, strings.TrimSpace(string(body)))
	}

	nextRefreshToken := strings.TrimSpace(tokenResp.RefreshToken)
	if nextRefreshToken == "" {
		// 部分实现只在轮换时下发新 refresh token，未下发时保留旧值。
		nextRefreshToken = refreshToken
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", fmt.Errorf("NextMoe refresh 响应缺少访问令牌")
	}

	s.config.NextMoeAccessToken = strings.TrimSpace(tokenResp.AccessToken)
	s.config.NextMoeRefreshToken = nextRefreshToken
	s.config.NextMoeTokenExpiresAt = s.now().Add(nextMoeTokenExpiryDuration(tokenResp.ExpiresIn)).Format(time.RFC3339)
	appconf.SanitizeNextMoeOAuthConfig(s.config)
	if err := appconf.SaveConfig(s.config); err != nil {
		return "", fmt.Errorf("保存 NextMoe 刷新后配置失败: %w", err)
	}

	status := s.buildAuthStatusLocked()
	s.emitAuthStatusChanged(status)
	applog.LogInfof(s.ctx, "NextMoe access token refreshed successfully")
	return s.config.NextMoeAccessToken, nil
}

func (s *NextMoeService) buildAuthStatusLocked() vo.NextMoeAuthStatus {
	if s.config == nil {
		return vo.NextMoeAuthStatus{}
	}
	accessToken := strings.TrimSpace(s.config.NextMoeAccessToken)
	refreshToken := strings.TrimSpace(s.config.NextMoeRefreshToken)
	return vo.NextMoeAuthStatus{
		Authorized:           accessToken != "" || refreshToken != "",
		AccountLabel:         strings.TrimSpace(s.config.NextMoeAccountLabel),
		AccessTokenExpiresAt: strings.TrimSpace(s.config.NextMoeTokenExpiresAt),
	}
}

func (s *NextMoeService) persistAuthorizedStateLocked(tokenResp *nextMoeTokenResponse) (vo.NextMoeAuthStatus, error) {
	if s.config == nil {
		return vo.NextMoeAuthStatus{}, fmt.Errorf("NextMoe 配置未初始化")
	}
	s.config.NextMoeAccessToken = strings.TrimSpace(tokenResp.AccessToken)
	s.config.NextMoeRefreshToken = strings.TrimSpace(tokenResp.RefreshToken)
	s.config.NextMoeTokenExpiresAt = s.now().Add(nextMoeTokenExpiryDuration(tokenResp.ExpiresIn)).Format(time.RFC3339)
	appconf.SanitizeNextMoeOAuthConfig(s.config)
	if err := appconf.SaveConfig(s.config); err != nil {
		return vo.NextMoeAuthStatus{}, fmt.Errorf("保存 NextMoe 授权配置失败: %w", err)
	}
	return s.buildAuthStatusLocked(), nil
}

func (s *NextMoeService) clearAuthorizationLocked() (vo.NextMoeAuthStatus, error) {
	if s.config == nil {
		return vo.NextMoeAuthStatus{}, fmt.Errorf("NextMoe 配置未初始化")
	}
	s.config.NextMoeAccessToken = ""
	s.config.NextMoeRefreshToken = ""
	s.config.NextMoeTokenExpiresAt = ""
	s.config.NextMoeAccountLabel = ""
	appconf.SanitizeNextMoeOAuthConfig(s.config)
	if err := appconf.SaveConfig(s.config); err != nil {
		return vo.NextMoeAuthStatus{}, fmt.Errorf("保存 NextMoe 配置失败: %w", err)
	}
	return s.buildAuthStatusLocked(), nil
}

func (s *NextMoeService) exchangeAuthorizationCode(ctx context.Context, code, redirectURI, codeVerifier string) (*nextMoeTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {nextMoeOAuthClientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}
	tokenResp, statusCode, body, err := s.requestToken(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("NextMoe token 交换失败: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("NextMoe OAuth 错误 %s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if statusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("NextMoe token 交换失败，HTTP %d: %s", statusCode, strings.TrimSpace(string(body)))
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" || strings.TrimSpace(tokenResp.RefreshToken) == "" {
		return nil, fmt.Errorf("NextMoe token 响应缺少必要字段")
	}
	return tokenResp, nil
}

func (s *NextMoeService) requestToken(ctx context.Context, form url.Values) (*nextMoeTokenResponse, int, []byte, error) {
	req, err := http.NewRequestWithContext(s.resolveContext(ctx), http.MethodPost, nextMoeOAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := s.doRequest(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, nil, err
	}
	var tokenResp nextMoeTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, resp.StatusCode, body, fmt.Errorf("解析令牌响应失败: %w", err)
	}
	return &tokenResp, resp.StatusCode, body, nil
}

func (s *NextMoeService) doRequest(req *http.Request) (*http.Response, error) {
	return httputils.DoWithRetry(req.Context(), s.httpClient, req, httputils.RetryPolicy{
		MaxRetries:    1,
		FallbackDelay: time.Second,
		MaxDelay:      30 * time.Second,
	})
}

func (s *NextMoeService) emitAuthStatusChanged(status vo.NextMoeAuthStatus) {
	if s.ctx == nil || s.emitEvent == nil {
		return
	}
	s.emitEvent(nextMoeAuthEventName, status)
}

func (s *NextMoeService) resolveContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// nextMoeTokenExpiryDuration 换算令牌有效期；服务端未下发时按 15 分钟兜底。
func nextMoeTokenExpiryDuration(expiresIn int) time.Duration {
	if expiresIn <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(expiresIn) * time.Second
}

func buildNextMoeAuthURL(session *nextMoeAuthSession) string {
	challengeBytes := sha256.Sum256([]byte(session.codeVerifier))
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {nextMoeOAuthClientID},
		"redirect_uri":          {session.redirectURI},
		"scope":                 {nextMoeOAuthScopes},
		"state":                 {session.state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challengeBytes[:])},
		"code_challenge_method": {"S256"},
	}
	return nextMoeOAuthAuthorizeURL + "?" + params.Encode()
}

func newNextMoeAuthSession() (*nextMoeAuthSession, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", nextMoeOAuthCallbackPort))
	if err != nil {
		return nil, fmt.Errorf("无法启动 NextMoe 本地回调服务: %w", err)
	}
	state, err := generateNextMoeRandomValue(16)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("生成 NextMoe OAuth state 失败: %w", err)
	}
	codeVerifier, err := generateNextMoeRandomValue(48)
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("生成 NextMoe PKCE verifier 失败: %w", err)
	}

	port := nextMoeOAuthCallbackPort
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		port = tcpAddr.Port
	}
	session := &nextMoeAuthSession{
		resultChan:   make(chan nextMoeAuthResult, 1),
		listener:     listener,
		state:        state,
		codeVerifier: codeVerifier,
		redirectURI:  fmt.Sprintf("http://127.0.0.1:%d%s", port, nextMoeOAuthCallbackPath),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(nextMoeOAuthCallbackPath, session.handleOAuthCallback)
	session.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if serveErr := session.server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			session.trySendResult(nextMoeAuthResult{Error: serveErr.Error()})
		}
	}()
	return session, nil
}

func generateNextMoeRandomValue(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *nextMoeAuthSession) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
	_ = s.listener.Close()
}

func (s *nextMoeAuthSession) trySendResult(result nextMoeAuthResult) {
	select {
	case s.resultChan <- result:
	default:
	}
}

func (s *nextMoeAuthSession) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>授权失败</title></head><body><h1>授权失败</h1><p>请求方法无效</p><p>您可以关闭此窗口。</p></body></html>`)
		return
	}
	if !isLoopbackRequest(r.RemoteAddr) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>授权失败</title></head><body><h1>授权失败</h1><p>回调来源无效</p><p>请返回应用后重试。</p></body></html>`)
		return
	}
	query := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(s.state)) != 1 {
		s.trySendResult(nextMoeAuthResult{Error: "授权状态校验失败"})
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>授权失败</title></head><body><h1>授权失败</h1><p>授权状态校验失败</p><p>请返回应用后重试。</p></body></html>`)
		return
	}
	if oauthError := strings.TrimSpace(query.Get("error")); oauthError != "" {
		description := strings.TrimSpace(query.Get("error_description"))
		s.trySendResult(nextMoeAuthResult{Error: strings.TrimSpace(oauthError + ": " + description)})
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>授权失败</title></head><body><h1>授权失败</h1><p>%s: %s</p><p>您可以关闭此窗口。</p></body></html>`, html.EscapeString(oauthError), html.EscapeString(description))
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	if code == "" {
		s.trySendResult(nextMoeAuthResult{Error: "未收到授权码"})
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>授权失败</title></head><body><h1>授权失败</h1><p>未收到授权码</p><p>您可以关闭此窗口。</p></body></html>`)
		return
	}
	s.trySendResult(nextMoeAuthResult{Code: code})
	fmt.Fprint(w, `<!DOCTYPE html><html><head><title>授权成功</title></head><body><h1>授权成功！</h1><p>您可以关闭此窗口并返回应用。</p><script>window.close();</script></body></html>`)
}
