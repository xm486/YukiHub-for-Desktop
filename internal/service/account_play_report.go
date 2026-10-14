package service

import (
	"context"
	"strings"
	"time"

	"yukihub/internal/applog"
	"yukihub/internal/service/yukihubaccount"
)

// 游玩时长上报：一次游玩会话结算后把它报给账号服务端，用于
//
//	① 发放游戏经验（+等级）；
//	② 计入网页端「每日任务」里的游玩时长进度。
//
// 对齐手机版的两个入口：
//   - 游戏库启动 → MainActivity.reportPlaySessionToServer
//   - 大屏启动   → BigScreenLauncher.reportPlaySessionToServer
//
// **桌面端这两个入口本来就走同一条链路**（都进 StartService.startGame），
// 所以只需要在会话结算处挂一个钩子，两处入口自动都算上 —— 不像手机端大屏是
// 独立实现、当初只调了本地 finishPlaySession 而漏了上报（用户反馈的就是这个）。
//
// 边界：只在「游戏进程退出 / 手动结束计时」这条活路径上上报。
// 应用退出时那条批量结算路径（CleanupPendingSessions）刻意不上报 ——
// 那时进程正在收尾，发一个可能完不成的网络请求既不可靠、又会拖慢退出。
// 该场景与「手机端 App 被杀」等价，手机端同样不会补报（本地记录仍在库里）。
const (
	playReportTimeout = 15 * time.Second
	// playReportMinDurationSeconds 与服务端规则、以及本地 `<60 秒直接删记录`
	// 保持一致：不足一分钟的会话两端都不计，没必要白发一次请求。
	//
	// 调用方（StartService.finalizePlaySessionOnce）在没有达到这个阈值时已经
	// 提前 return 了，这里再判一次是**不变量守卫**：这条上报链路的入口只此一处，
	// 但阈值散在两处时最容易出现「改了一处忘了另一处」。
	playReportMinDurationSeconds = 60
)

// playSessionReporter 是「会话结算后上报服务端」的接收方。
//
// 定义成窄接口（且方法名不导出）有两个目的：
//   - StartService 的单测可以塞一个记账替身，不必拉起真的 AccountService；
//   - 不导出 ⇒ 只有本包能实现，也绝不会被当成前端 API 绑出去。
type playSessionReporter interface {
	reportPlaySession(sessionID, gameTitle string, startTime time.Time, durationSeconds int)
}

// reportPlaySession 把一次已结算的会话上报到服务端。**尽力而为**：
// 失败只留日志，绝不向调用方返回错误，也绝不阻塞游戏退出流程。
//
// 与手机版一致：未登录直接跳过（服务端只会回 401，白跑一趟）。
func (s *AccountService) reportPlaySession(sessionID, gameTitle string, startTime time.Time, durationSeconds int) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if !s.isLoggedIn() {
		return
	}
	if durationSeconds < playReportMinDurationSeconds {
		return
	}

	// 上报的结束时间由「起始时间 + 本地结算时长」推出，而不是墙钟的「现在」。
	// 这与本地落库口径完全一致（见 SessionService.UpdatePlaySession 同样按
	// start_time + duration 重算 end_time）：开启「仅记录活跃时长」时，
	// 墙钟跨度会明显大于真实游玩时长，按墙钟发等于向服务端虚报每日任务时长。
	endTime := startTime.Add(time.Duration(durationSeconds) * time.Second)

	// 用独立上下文：应用退出时 s.ctx 会被取消，而这次上报可能刚好在收尾阶段发出。
	ctx, cancel := context.WithTimeout(context.Background(), playReportTimeout)
	defer cancel()

	result, err := accountFetch(s, func(token string) (yukihubaccount.PlayReportResult, error) {
		// gameKey 目前两端都发空串（服务端按 sessionUuid 记账）；
		// sessionUuid 直接复用 play_sessions.id（创建时生成的 UUID），
		// 它天然唯一且稳定 —— 重试/补报不会重复计经验。
		return s.client.ReportPlayTime(ctx, token, sessionID, "", gameTitle, startTime, endTime)
	})
	if err != nil {
		// 不阻塞游戏退出流程，但要留痕：用户开日志时能查到上报失败原因。
		applog.LogWarningf(s.ctx, "游玩时长上报失败（session=%s）：%v", sessionID, err)
		return
	}
	// ExpAwarded 为 0 是正常情况（时长不足 / 当日额度已满），不是错误。
	if result.ExpAwarded > 0 {
		applog.LogInfof(s.ctx, "游玩时长上报成功：session=%s exp+%d level=%d", sessionID, result.ExpAwarded, result.Level)
	}
}
