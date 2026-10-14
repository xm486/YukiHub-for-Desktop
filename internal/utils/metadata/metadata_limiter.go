package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"yukihub/internal/common/enums"
	"yukihub/internal/utils/httputils"
)

type MetadataSource = enums.SourceType

const metadataDefaultMaxRateLimitRetries = 1

type MetadataRateLimitPolicy struct {
	Source         MetadataSource
	Interval       time.Duration
	UpstreamLimit  int
	UpstreamWindow time.Duration
	// AnonymousInterval / AnonymousUpstreamLimit 只在「本次请求未携带凭据」的档位生效。
	// 0 表示该来源没有匿名档差别，沿用 Interval / UpstreamLimit。
	//
	// 为什么必须分档：NextMoe 的配额按身份计（匿名按出口 IP 50/min，
	// 登录后按用户 100/min）。如果两档共用同一个窗口计数器，登录用户会被
	// 匿名档的 50/min 卡住 —— 那就是「登录了额度还是一样」，属于必须避免的 bug。
	AnonymousInterval      time.Duration
	AnonymousUpstreamLimit int
	RateLimitRetryDelay    time.Duration
	MaxRetryDelay          time.Duration
	MaxRateLimitRetries    int
}

func DefaultMetadataRateLimitPolicies() map[MetadataSource]MetadataRateLimitPolicy {
	policies := map[MetadataSource]MetadataRateLimitPolicy{
		enums.Bangumi: {
			Source:         enums.Bangumi,
			Interval:       time.Second,
			UpstreamLimit:  3000,
			UpstreamWindow: 10 * time.Minute,
		},
		enums.VNDB: {
			Source:              enums.VNDB,
			Interval:            4 * time.Second,
			UpstreamLimit:       200,
			UpstreamWindow:      5 * time.Minute,
			RateLimitRetryDelay: time.Minute,
			MaxRetryDelay:       5 * time.Minute,
			MaxRateLimitRetries: 4,
		},
		enums.Ymgal: {
			Source:   enums.Ymgal,
			Interval: 2 * time.Second,
		},
		enums.Steam: {
			Source:   enums.Steam,
			Interval: time.Second,
		},
		enums.DLsite: {
			Source:   enums.DLsite,
			Interval: 2 * time.Second,
		},
		enums.TouchGal: {
			Source:   enums.TouchGal,
			Interval: 2 * time.Second,
		},
		// 上游按**应用**限速 60 次/分钟。注意这个配额是「所有装了 YukiHub 的机器
		// 共用」的 —— 元数据走的是内置的应用级凭据（见 metadata_hikarinagi.go），
		// 不是每个用户一份。本机限到 60/分钟只能保证自己不超；
		// 多台机器同时刮削时仍会撞限，所以退避必须配足：
		// 默认退避是「等 1 秒重试 1 次」，对 1 分钟窗口毫无意义，撞上就是直接失败。
		enums.Hikarinagi: {
			Source:              enums.Hikarinagi,
			Interval:            time.Second,
			UpstreamLimit:       60,
			UpstreamWindow:      time.Minute,
			RateLimitRetryDelay: time.Minute,
			MaxRetryDelay:       2 * time.Minute,
			MaxRateLimitRetries: 2,
		},
		enums.ErogameScape: {
			Source:   enums.ErogameScape,
			Interval: 2 * time.Second,
		},
		// 镜像站与主站同一套 API、同一份配额口径。
		enums.BangumiMirror: {
			Source:         enums.BangumiMirror,
			Interval:       time.Second,
			UpstreamLimit:  3000,
			UpstreamWindow: 10 * time.Minute,
		},
		// NextMoe（未萌）配额按身份计：
		//   - 登录档（带用户令牌）：100/min、10,000/UTC 日，按用户计 → 间隔 700ms
		//   - 匿名档（不带 Authorization）：50/min，按出口 IP 计 → 间隔 1300ms
		// 数值逐项对齐手机版 NextMoeClient.MIN_INTERVAL_AUTHED_MS / MIN_INTERVAL_ANON_MS。
		// 客户端限速是「不给对方服务器添压力」的第一道闸，重试退避是第二道。
		enums.NextMoe: {
			Source:                 enums.NextMoe,
			Interval:               700 * time.Millisecond,
			UpstreamLimit:          100,
			UpstreamWindow:         time.Minute,
			AnonymousInterval:      1300 * time.Millisecond,
			AnonymousUpstreamLimit: 50,
			RateLimitRetryDelay:    1200 * time.Millisecond,
			MaxRetryDelay:          5 * time.Second,
			MaxRateLimitRetries:    2,
		},
	}

	return policies
}

// metadataLimiterKey 区分「同一来源的匿名档与登录档」。
//
// 匿名与登录是两套独立的额度（分别按出口 IP 与按用户计），必须各自维护窗口，
// 否则登录用户的额度会被匿名档的历史请求占掉（手机版也是按档位取间隔，
// 只是在移动端只有一条串行刮削链路，差异看不出来）。
type metadataLimiterKey struct {
	source    MetadataSource
	anonymous bool
}

type metadataRateLimiter struct {
	mu       sync.Mutex
	policies map[MetadataSource]MetadataRateLimitPolicy
	sources  map[metadataLimiterKey]*metadataSourceLimiter
	now      func() time.Time
	wait     func(context.Context, time.Duration) error
}

type metadataSourceLimiter struct {
	mu            sync.Mutex
	nextAllowedAt time.Time
	requestTimes  []time.Time
}

var sharedMetadataRateLimiter = newMetadataRateLimiter(DefaultMetadataRateLimitPolicies())

func newMetadataRateLimiter(policies map[MetadataSource]MetadataRateLimitPolicy) *metadataRateLimiter {
	copied := make(map[MetadataSource]MetadataRateLimitPolicy, len(policies))
	for source, policy := range policies {
		copied[source] = policy
	}

	return &metadataRateLimiter{
		policies: copied,
		sources:  make(map[metadataLimiterKey]*metadataSourceLimiter, len(copied)),
		now:      time.Now,
		wait:     contextAwareSleep,
	}
}

func (l *metadataRateLimiter) Acquire(ctx context.Context, source MetadataSource) error {
	return l.acquire(ctx, source, false)
}

// AcquireForTier 按档位取用配额：anonymous=true 表示本次请求不带凭据，
// 走 AnonymousInterval / AnonymousUpstreamLimit（未配置时自动沿用登录档）。
func (l *metadataRateLimiter) AcquireForTier(ctx context.Context, source MetadataSource, anonymous bool) error {
	return l.acquire(ctx, source, anonymous)
}

func (l *metadataRateLimiter) acquire(ctx context.Context, source MetadataSource, anonymous bool) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	policy, sourceLimiter, ok := l.sourceState(source, anonymous)
	if !ok || !policy.hasLimit() {
		return nil
	}

	sourceLimiter.mu.Lock()
	defer sourceLimiter.mu.Unlock()

	for {
		now := l.now()
		waitFor := sourceLimiter.nextWaitDuration(policy, now)
		if waitFor <= 0 {
			sourceLimiter.reserve(policy, now)
			return nil
		}
		if err := l.wait(ctx, waitFor); err != nil {
			return err
		}
	}
}

// resolve 把档位换算成实际生效的间隔与窗口额度。
func (p MetadataRateLimitPolicy) forAnonymousTier() MetadataRateLimitPolicy {
	if p.AnonymousInterval > 0 {
		p.Interval = p.AnonymousInterval
	}
	if p.AnonymousUpstreamLimit > 0 {
		p.UpstreamLimit = p.AnonymousUpstreamLimit
	}
	return p
}

func (p MetadataRateLimitPolicy) hasLimit() bool {
	return p.Interval > 0 || (p.UpstreamLimit > 0 && p.UpstreamWindow > 0)
}

func (l *metadataRateLimiter) Policy(source MetadataSource) (MetadataRateLimitPolicy, bool) {
	if l == nil {
		return MetadataRateLimitPolicy{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	policy, ok := l.policies[source]
	return policy, ok
}

func (l *metadataRateLimiter) sourceState(source MetadataSource, anonymous bool) (MetadataRateLimitPolicy, *metadataSourceLimiter, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	policy, ok := l.policies[source]
	if !ok {
		return MetadataRateLimitPolicy{}, nil, false
	}
	if anonymous {
		policy = policy.forAnonymousTier()
	}
	key := metadataLimiterKey{source: source, anonymous: anonymous}
	sourceLimiter := l.sources[key]
	if sourceLimiter == nil {
		sourceLimiter = &metadataSourceLimiter{}
		l.sources[key] = sourceLimiter
	}
	return policy, sourceLimiter, true
}

func (l *metadataSourceLimiter) nextWaitDuration(policy MetadataRateLimitPolicy, now time.Time) time.Duration {
	var waitFor time.Duration
	if policy.Interval > 0 {
		waitFor = maxMetadataWait(waitFor, l.nextAllowedAt.Sub(now))
	}
	if policy.UpstreamLimit > 0 && policy.UpstreamWindow > 0 {
		l.pruneRequestTimes(now.Add(-policy.UpstreamWindow))
		if len(l.requestTimes) >= policy.UpstreamLimit {
			waitFor = maxMetadataWait(waitFor, l.requestTimes[0].Add(policy.UpstreamWindow).Sub(now))
		}
	}
	return waitFor
}

func (l *metadataSourceLimiter) reserve(policy MetadataRateLimitPolicy, now time.Time) {
	if policy.Interval > 0 {
		l.nextAllowedAt = now.Add(policy.Interval)
	}
	if policy.UpstreamLimit > 0 && policy.UpstreamWindow > 0 {
		l.requestTimes = append(l.requestTimes, now)
	}
}

func (l *metadataSourceLimiter) pruneRequestTimes(cutoff time.Time) {
	keepFrom := 0
	for keepFrom < len(l.requestTimes) && !l.requestTimes[keepFrom].After(cutoff) {
		keepFrom++
	}
	if keepFrom == 0 {
		return
	}
	copy(l.requestTimes, l.requestTimes[keepFrom:])
	l.requestTimes = l.requestTimes[:len(l.requestTimes)-keepFrom]
}

func maxMetadataWait(current time.Duration, candidate time.Duration) time.Duration {
	if candidate > current {
		return candidate
	}
	return current
}

func contextAwareSleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func doLimitedMetadataRequest(client *http.Client, req *http.Request, source MetadataSource) (*http.Response, error) {
	if client == nil {
		return nil, errors.New("metadata HTTP client is nil")
	}
	if req == nil {
		return nil, errors.New("metadata HTTP request is nil")
	}

	maxRetries := metadataDefaultMaxRateLimitRetries
	if policy, ok := sharedMetadataRateLimiter.Policy(source); ok && policy.MaxRateLimitRetries > 0 {
		maxRetries = policy.MaxRateLimitRetries
	}

	resp, err := httputils.DoWithRetry(req.Context(), client, req, httputils.RetryPolicy{
		MaxRetries: maxRetries,
		RetryableStatus: func(statusCode int) bool {
			return statusCode == http.StatusTooManyRequests
		},
		BeforeAttempt: func(ctx context.Context, _ int) error {
			if err := sharedMetadataRateLimiter.Acquire(ctx, source); err != nil {
				return fmt.Errorf("%s metadata rate limit wait failed: %w", source, err)
			}
			return nil
		},
		RetryDelay: func(resp *http.Response, retryIndex int) time.Duration {
			return metadataRateLimitRetryDelay(source, resp.Header.Get("Retry-After"), retryIndex)
		},
		Wait: func(ctx context.Context, delay time.Duration) error {
			if err := sharedMetadataRateLimiter.wait(ctx, delay); err != nil {
				return fmt.Errorf("%s metadata retry wait failed: %w", source, err)
			}
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	closeResponseBody(resp.Body)
	return nil, fmt.Errorf("%s metadata request remained rate limited after %d retries: status %d, body: %s", source, maxRetries, resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
}

func doLimitedMetadataRequestBody(client *http.Client, req *http.Request, source MetadataSource) (int, http.Header, []byte, error) {
	resp, err := doLimitedMetadataRequest(client, req, source)
	if err != nil {
		return 0, nil, nil, err
	}
	defer closeResponseBody(resp.Body)

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, bodyBytes, nil
}

func metadataRateLimitRetryDelay(source MetadataSource, retryAfter string, retryIndex int) time.Duration {
	serverDelay, _ := httputils.ParseRetryAfter(retryAfter, sharedMetadataRateLimiter.now())
	policy, ok := sharedMetadataRateLimiter.Policy(source)
	if !ok {
		return serverDelay
	}

	delay := policy.RateLimitRetryDelay
	if delay <= 0 {
		delay = policy.Interval
	}
	for i := 0; i < retryIndex && delay > 0; i++ {
		if policy.MaxRetryDelay > 0 && delay >= policy.MaxRetryDelay/2 {
			delay = policy.MaxRetryDelay
			break
		}
		delay *= 2
	}
	if policy.MaxRetryDelay > 0 && delay > policy.MaxRetryDelay {
		delay = policy.MaxRetryDelay
	}
	if serverDelay > delay {
		return serverDelay
	}
	return delay
}

func waitForMetadataRateLimitBackoff(ctx context.Context, source MetadataSource, retryAfter string, retryIndex int) error {
	delay := metadataRateLimitRetryDelay(source, retryAfter, retryIndex)
	if delay <= 0 {
		return nil
	}
	if err := sharedMetadataRateLimiter.wait(ctx, delay); err != nil {
		return fmt.Errorf("%s metadata retry wait failed: %w", source, err)
	}
	return nil
}

// waitForMetadataRateLimitRetry preserves the single-retry behavior used by APIs
// that report throttling in a successful response body instead of HTTP 429.
func waitForMetadataRateLimitRetry(ctx context.Context, source MetadataSource, retryAfter string) error {
	return waitForMetadataRateLimitBackoff(ctx, source, retryAfter, 0)
}

// cloneMetadataRequest supports Ymgal token refresh and successful responses
// that carry a rate-limit code in the response body.
func cloneMetadataRequest(req *http.Request) (*http.Request, error) {
	if req == nil {
		return nil, errors.New("metadata HTTP request is nil")
	}

	cloned := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		cloned.Body = nil
		return cloned, nil
	}
	if req.GetBody == nil {
		return nil, errors.New("metadata request body cannot be replayed")
	}

	body, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("recreate metadata request body: %w", err)
	}
	cloned.Body = body
	return cloned, nil
}

func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	return strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate limited") ||
		strings.Contains(message, "too many requests") ||
		strings.Contains(message, "status 429") ||
		strings.Contains(message, "status: 429") ||
		strings.Contains(message, "http 429") ||
		strings.Contains(message, "limit exceeded") ||
		strings.Contains(message, "throttle")
}
