package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"yukihub/internal/common/enums"
)

// ------------------------ 档位（匿名 / 登录） ------------------------

// 手机版 NextMoeClient 的两个间隔：匿名 1300ms（按出口 IP 50/min）、
// 登录 700ms（按用户 100/min）。这套数值是「登录真的能提额」的量化口径。
func TestNextMoeDefaultPolicyRaisesQuotaWhenAuthenticated(t *testing.T) {
	policy, ok := DefaultMetadataRateLimitPolicies()[enums.NextMoe]
	if !ok {
		t.Fatal("expected NextMoe policy")
	}
	if policy.Interval != 700*time.Millisecond {
		t.Fatalf("登录档间隔应为 700ms，实际 %s", policy.Interval)
	}
	if policy.AnonymousInterval != 1300*time.Millisecond {
		t.Fatalf("匿名档间隔应为 1300ms，实际 %s", policy.AnonymousInterval)
	}
	if policy.UpstreamLimit != 100 {
		t.Fatalf("登录档窗口额度应为 100，实际 %d", policy.UpstreamLimit)
	}
	if policy.AnonymousUpstreamLimit != 50 {
		t.Fatalf("匿名档窗口额度应为 50，实际 %d", policy.AnonymousUpstreamLimit)
	}
	// 登录档必须**更宽**，否则就是「登录了额度还是一样」。
	if policy.Interval >= policy.AnonymousInterval {
		t.Fatalf("登录档间隔 %s 未高于匿名档 %s", policy.Interval, policy.AnonymousInterval)
	}
	if policy.UpstreamLimit <= policy.AnonymousUpstreamLimit {
		t.Fatalf("登录档额度 %d 未高于匿名档 %d", policy.UpstreamLimit, policy.AnonymousUpstreamLimit)
	}
}

// 逐次取号量出的稳态间隔：匿名档 1300ms、登录档 700ms。
// 只配 Interval/AnonymousInterval（不配窗口），这样量到的等待就是纯档位间隔。
func TestNextMoeThrottleTiersUseTheirOwnIntervals(t *testing.T) {
	limiter, clock := newInstantMetadataRateLimiter(map[MetadataSource]MetadataRateLimitPolicy{
		enums.NextMoe: {
			Source:            enums.NextMoe,
			Interval:          700 * time.Millisecond,
			AnonymousInterval: 1300 * time.Millisecond,
		},
	})
	ctx := context.Background()

	for index := 0; index < 3; index++ {
		if err := limiter.AcquireForTier(ctx, enums.NextMoe, true); err != nil {
			t.Fatalf("匿名档取号失败: %v", err)
		}
	}
	for index := 0; index < 3; index++ {
		if err := limiter.AcquireForTier(ctx, enums.NextMoe, false); err != nil {
			t.Fatalf("登录档取号失败: %v", err)
		}
	}

	// 每档第一次取号无需等待，之后两次各自按档位间隔等待。
	want := []time.Duration{
		1300 * time.Millisecond, 1300 * time.Millisecond,
		700 * time.Millisecond, 700 * time.Millisecond,
	}
	got := clock.recorded()
	if len(got) != len(want) {
		t.Fatalf("unexpected waits: got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("wait[%d] = %s, want %s (all waits: %v)", index, got[index], want[index], got)
		}
	}
}

// 两档必须各持一份限速状态。共用一份的话，登录用户的窗口会被匿名档的历史请求占掉，
// 表现出来就是「登录了额度还是一样」。
func TestNextMoeThrottleTiersKeepSeparateState(t *testing.T) {
	limiter, _ := newInstantMetadataRateLimiter(DefaultMetadataRateLimitPolicies())
	ctx := context.Background()

	if err := limiter.AcquireForTier(ctx, enums.NextMoe, true); err != nil {
		t.Fatalf("匿名档取号失败: %v", err)
	}
	if err := limiter.AcquireForTier(ctx, enums.NextMoe, false); err != nil {
		t.Fatalf("登录档取号失败: %v", err)
	}

	if len(limiter.sources) != 2 {
		t.Fatalf("expected 2 independent limiter states, got %d", len(limiter.sources))
	}
	for _, anonymous := range []bool{true, false} {
		key := metadataLimiterKey{source: enums.NextMoe, anonymous: anonymous}
		if _, ok := limiter.sources[key]; !ok {
			t.Fatalf("missing limiter state for anonymous=%v", anonymous)
		}
	}
}

// 非 NextMoe 来源没有匿名档差异，Acquire 与 AcquireForTier 必须等价（不影响其它来源）。
func TestMetadataRateLimiterTierDefaultsToAuthenticatedPolicy(t *testing.T) {
	limiter, _ := newInstantMetadataRateLimiter(map[MetadataSource]MetadataRateLimitPolicy{
		enums.VNDB: {
			Source:   enums.VNDB,
			Interval: time.Second,
		},
	})

	for _, anonymous := range []bool{false, true} {
		policy, _, ok := limiter.sourceState(enums.VNDB, anonymous)
		if !ok {
			t.Fatalf("missing VNDB policy for anonymous=%v", anonymous)
		}
		if policy.Interval != time.Second {
			t.Fatalf("VNDB 无匿名档，间隔应恒为 1s，anonymous=%v 时得到 %s", anonymous, policy.Interval)
		}
	}
}

// ------------------------ HTTP 行为 ------------------------

func TestNextMoeAnonymousRequestOmitsAuthorizationHeader(t *testing.T) {
	var seen []string
	getter := newNextMoeTestGetter(t, func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.Header.Get("Authorization"))
		return metadataJSONResponse(req, http.StatusOK, `{"object":"work","id":"42","display_name":"A"}`), nil
	})

	// 匿名：空令牌必须能正常取到数据，而不是「未授权」错误。
	if _, err := getter.FetchMetadata("42", ""); err != nil {
		t.Fatalf("匿名取详情失败: %v", err)
	}
	// 登录：必须带上 Bearer —— 带上才算按用户计配额。
	if _, err := getter.FetchMetadata("42", "tok-1"); err != nil {
		t.Fatalf("登录取详情失败: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(seen))
	}
	if seen[0] != "" {
		t.Fatalf("匿名请求不应带 Authorization，实际 %q", seen[0])
	}
	if seen[1] != "Bearer tok-1" {
		t.Fatalf("登录请求应带 Bearer 令牌，实际 %q", seen[1])
	}
}

func TestNextMoeDailyQuotaExceededDoesNotRetry(t *testing.T) {
	requestCount := 0
	getter := newNextMoeTestGetter(t, func(req *http.Request) (*http.Response, error) {
		requestCount++
		return metadataJSONResponse(req, http.StatusTooManyRequests, `{"code":"QUOTA_EXCEEDED"}`), nil
	})

	_, err := getter.FetchMetadata("42", "")
	if !IsNextMoeQuotaExceededError(err) {
		t.Fatalf("expected quota-exceeded error, got %v", err)
	}
	// 日配额耗尽等下去不会恢复：必须一次就放弃，不许退避重试。
	if requestCount != 1 {
		t.Fatalf("quota exceeded 不应重试，实际发了 %d 次请求", requestCount)
	}
	// 匿名档的报错要带上「怎么提额」，登录档不带。
	if !strings.Contains(err.Error(), "登录未萌账号可提升抓取配额") {
		t.Fatalf("匿名档错误应提示可登录提额，实际 %v", err)
	}
}

func TestNextMoeMinuteWindowRetriesThenFails(t *testing.T) {
	requestCount := 0
	getter := newNextMoeTestGetter(t, func(req *http.Request) (*http.Response, error) {
		requestCount++
		resp := metadataJSONResponse(req, http.StatusTooManyRequests, `{"code":"RATE_LIMITED"}`)
		resp.Header.Set("Retry-After", "3")
		return resp, nil
	})

	_, err := getter.FetchMetadata("42", "tok")
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	if IsNextMoeQuotaExceededError(err) {
		t.Fatalf("分钟窗限流不应被判成日配额耗尽: %v", err)
	}
	// 1 次首发 + 2 次重试。
	if requestCount != 3 {
		t.Fatalf("expected 3 attempts, got %d", requestCount)
	}
	// 登录档受限是账号自己的额度用完，不该再劝他去登录。
	if strings.Contains(err.Error(), "登录未萌账号可提升抓取配额") {
		t.Fatalf("登录档错误不该带匿名提额提示，实际 %v", err)
	}
}

func TestNextMoeUnauthorizedIsReportedForTokenRefresh(t *testing.T) {
	getter := newNextMoeTestGetter(t, func(req *http.Request) (*http.Response, error) {
		return metadataJSONResponse(req, http.StatusUnauthorized, `{"code":"UNAUTHORIZED"}`), nil
	})

	_, err := getter.FetchMetadata("42", "expired")
	if !IsNextMoeUnauthorizedError(err) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestNextMoeFollowsEntityMergedRedirect(t *testing.T) {
	var paths []string
	getter := newNextMoeTestGetter(t, func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if strings.HasSuffix(req.URL.Path, "/old-1") {
			return metadataJSONResponse(req, http.StatusNotFound, `{"code":"ENTITY_MERGED","current_id":"new-1"}`), nil
		}
		return metadataJSONResponse(req, http.StatusOK, `{"object":"work","id":"new-1","display_name":"A"}`), nil
	})

	result, err := getter.FetchMetadata("old-1", "")
	if err != nil {
		t.Fatalf("合并重定向应被自动跟随: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 requests (old id + new id), got %d", len(paths))
	}
	if !strings.HasSuffix(paths[1], "/new-1") {
		t.Fatalf("第二次请求应打到新 id，实际 %s", paths[1])
	}
	if result.Game.SourceID != "new-1" {
		t.Fatalf("expected merged result to carry the new id, got %q", result.Game.SourceID)
	}
}

func TestNextMoeRetryAfterIsCappedAt60Seconds(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", 2 * time.Second},
		{"0", 2 * time.Second},
		{"not-a-number", 2 * time.Second},
		{"3", 3 * time.Second},
		{"600", nextMoeMaxRetryAfter},
	}
	for _, tc := range cases {
		if got := nextMoeRetryAfter(tc.raw); got != tc.want {
			t.Fatalf("nextMoeRetryAfter(%q) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

// 429 的退避必须是「Retry-After **加上** 这一轮的指数退避」，不能是取两者较大值。
//
// 手机版是叠加的（429 分支先睡 Retry-After，抛出后外层再睡 RETRY_DELAY_MS×n）。
// 写成取较大值会在服务端给了 Retry-After 时**丢掉**指数退避，比手机版更激进 ——
// 而这套退避的作用恰恰是「不给对方服务器添压力」。
func TestNextMoeRetryBackoffAddsRetryAfterOnTopOfExponentialDelay(t *testing.T) {
	requestCount := 0
	getter, clock := newNextMoeTestGetterWithClock(t, func(req *http.Request) (*http.Response, error) {
		requestCount++
		resp := metadataJSONResponse(req, http.StatusTooManyRequests, `{"code":"RATE_LIMITED"}`)
		resp.Header.Set("Retry-After", "5")
		return resp, nil
	})

	if _, err := getter.FetchMetadata("42", "tok"); err == nil {
		t.Fatal("重试耗尽后应返回错误")
	}
	if requestCount != 3 {
		t.Fatalf("1 次首发 + 2 次重试 = 3 次请求，实际 %d 次", requestCount)
	}

	// 第 n 次重试前等待 = Retry-After + nextMoeRetryDelay×(n+1)。
	want := []time.Duration{
		5*time.Second + nextMoeRetryDelay,
		5*time.Second + 2*nextMoeRetryDelay,
	}
	got := clock.recorded()
	if len(got) != len(want) {
		t.Fatalf("重试等待次数不对：got %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("第 %d 次退避 = %s, want %s（全部：%v）", index+1, got[index], want[index], got)
		}
	}
}

func TestIsRateLimitErrorCoversNextMoeThrottleMessage(t *testing.T) {
	// NextMoe 的分钟窗限流消息要能被既有的限流判定识别（重扫/批处理会据此退避）。
	err := errors.New("nextmoe API rate limited: status 429")
	if !IsRateLimitError(err) {
		t.Fatalf("expected %q to be treated as a rate limit error", err.Error())
	}
}

// ------------------------ 测试脚手架 ------------------------

// newInstantMetadataRateLimiter 返回一个「不真的睡」的限速器：
// now/wait 都被替换成在同一张假时钟上推进，于是档位间隔可以被断言成精确数值，
// 而测试本身不消耗真实时间。
func newInstantMetadataRateLimiter(policies map[MetadataSource]MetadataRateLimitPolicy) (*metadataRateLimiter, *instantClock) {
	clock := &instantClock{current: time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)}
	limiter := newMetadataRateLimiter(policies)
	limiter.now = clock.now
	limiter.wait = clock.wait
	return limiter, clock
}

type instantClock struct {
	mu      sync.Mutex
	current time.Time
	waits   []time.Duration
}

func (c *instantClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

func (c *instantClock) wait(_ context.Context, delay time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if delay > 0 {
		c.waits = append(c.waits, delay)
		c.current = c.current.Add(delay)
	}
	return nil
}

func (c *instantClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func newNextMoeTestGetter(t *testing.T, transport metadataRoundTripperFunc) *NextMoeInfoGetter {
	t.Helper()

	getter, _ := newNextMoeTestGetterWithClock(t, transport)
	return getter
}

// newNextMoeTestGetterWithClock 额外把假时钟交出来，供断言「退避等了多久」用：
// 限速器被替换成不真睡的实例，所以这些等待不会让测试真的卡住。
func newNextMoeTestGetterWithClock(t *testing.T, transport metadataRoundTripperFunc) (*NextMoeInfoGetter, *instantClock) {
	t.Helper()

	limiter, clock := newInstantMetadataRateLimiter(DefaultMetadataRateLimitPolicies())
	return NewNextMoeInfoGetter(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithNextMoeBaseURL("https://nextmoe.test/v2"),
		withMetadataRateLimiter(limiter),
	), clock
}

func metadataJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
