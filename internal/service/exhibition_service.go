package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"yukihub/internal/applog"
	"yukihub/internal/service/gamehelper"
)

// ExhibitionService 为「3D 展厅」提供本机库存快照。
//
// 为什么单独一个服务、而不是让前端自己拼：
// 展厅页面是手机版 assets/exhibition 的原样移植，它通过
// `window.ExhibitionBridge.getMyLibrary()` **同步**拿一个 JSON 字符串
// （手机版是 Android @JavascriptInterface，返回值就是 String）。
// 桌面端的 Wails 绑定是异步的，所以由这里一次性产出与手机版**同构**的
// JSON，前端预取后把字符串原样喂给页面 —— 页面侧解析代码零改动。
//
// 只读、纯本地：不上传任何东西（与手机版 ExhibitionBridge 的注释一致）。
type ExhibitionService struct {
	ctx context.Context
	db  *sql.DB
}

// NewExhibitionService 创建展厅数据服务。
func NewExhibitionService() *ExhibitionService {
	return &ExhibitionService{}
}

// Init 注入运行期依赖。
//
//wails:ignore
func (s *ExhibitionService) Init(ctx context.Context, db *sql.DB) {
	s.ctx = ctx
	s.db = db
}

// exhibitionGame 是页面侧消费的一条展品。
//
// 字段名必须与手机版 ExhibitionBridge.getMyLibrary 的 JSON 完全一致：
// hall.js / picker.js / viewer.js 都是按这些键名取值的。
// 单位也照抄手机版：totalPlayTime / lastPlayedAt 都是**毫秒**。
type exhibitionGame struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"originalTitle"`
	Engine        string `json:"engine"`
	PlayStatus    string `json:"playStatus"`
	TotalPlayTime int64  `json:"totalPlayTime"`
	LastPlayedAt  int64  `json:"lastPlayedAt"`
	Favorite      bool   `json:"favorite"`
	NSFW          bool   `json:"nsfw"`
	Tags          string `json:"tags"`
	HasCover      bool   `json:"hasCover"`
	Cover         string `json:"cover,omitempty"`
}

type exhibitionLibrary struct {
	Games []exhibitionGame `json:"games"`
	Count int              `json:"count"`
}

// GetLibraryJSON 返回展厅所需的库存快照。
//
// 与手机版 getMyLibrary 的差异只有两处，且都是桌面端环境所致：
//   - engine 恒为空串：桌面端不记录引擎（手机版有 engine 列）；
//   - cover 是同源 URL（/local/... 或 /proxy/image?url=...），
//     手机版是 https://exhibition.local/cover/<id> 由原生拦截直出。
func (s *ExhibitionService) GetLibraryJSON() (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("数据库未初始化")
	}

	games, err := s.loadGames()
	if err != nil {
		return "", err
	}

	payload := exhibitionLibrary{Games: games, Count: len(games)}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化展厅库存失败: %w", err)
	}
	return string(data), nil
}

func (s *ExhibitionService) loadGames() ([]exhibitionGame, error) {
	// 隐藏游戏不进展厅：与游戏库默认 exclude_hidden 一致，
	// 玩家把游戏藏起来就是不想再看到它。
	rows, err := s.db.QueryContext(s.ctx, `
		SELECT g.id,
		       COALESCE(g.name, ''),
		       COALESCE(g.aliases, '[]'),
		       COALESCE(g.cover_url, ''),
		       COALESCE(g.status, 'unplayed'),
		       COALESCE(g.is_nsfw, FALSE),
		       latest.last_played_at,
		       CASE WHEN EXISTS (
		           SELECT 1 FROM game_categories gc
		           WHERE gc.game_id = g.id AND gc.category_id = ?
		       ) THEN TRUE ELSE FALSE END AS favorite
		FROM games g
		LEFT JOIN (
			SELECT game_id, MAX(start_time) AS last_played_at
			FROM play_sessions
			GROUP BY game_id
		) latest ON latest.game_id = g.id
		WHERE COALESCE(g.hidden, FALSE) = FALSE
		ORDER BY g.name
	`, gamehelper.SystemFavoritesCategoryID)
	if err != nil {
		return nil, fmt.Errorf("查询展厅库存失败: %w", err)
	}
	defer rows.Close()

	games := make([]exhibitionGame, 0, 64)
	for rows.Next() {
		var (
			game         exhibitionGame
			aliasesJSON  string
			status       string
			lastPlayedAt sql.NullTime
		)
		if err := rows.Scan(
			&game.ID,
			&game.Title,
			&aliasesJSON,
			&game.Cover,
			&status,
			&game.NSFW,
			&lastPlayedAt,
			&game.Favorite,
		); err != nil {
			return nil, fmt.Errorf("读取展厅库存行失败: %w", err)
		}

		game.PlayStatus = normalizeExhibitionStatus(status)
		game.OriginalTitle = firstAlias(aliasesJSON, game.Title)
		game.Cover = normalizeExhibitionCoverURL(game.Cover)
		game.HasCover = game.Cover != ""
		if lastPlayedAt.Valid {
			game.LastPlayedAt = lastPlayedAt.Time.UnixMilli()
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历展厅库存失败: %w", err)
	}

	playTimes, err := gamehelper.QueryGamesPlayTime(s.ctx, s.db, nil)
	if err != nil {
		// 时长只是展示用，取不到不该让整个展厅打不开
		applog.LogWarningf(s.ctx, "展厅：统计游玩时长失败，全部按 0 处理: %v", err)
		playTimes = nil
	}
	tags, err := s.loadTags()
	if err != nil {
		applog.LogWarningf(s.ctx, "展厅：读取标签失败: %v", err)
		tags = nil
	}

	for i := range games {
		// 库内是秒，页面要毫秒（手机版 totalPlayTime 就是毫秒）
		games[i].TotalPlayTime = playTimes[games[i].ID] * 1000
		games[i].Tags = tags[games[i].ID]
	}
	return games, nil
}

// loadTags 一次读出全部标签并按游戏拼成一行文本。
// 逐游戏查询在千级库存下会变成 N 次往返，这里坚持一次扫完。
func (s *ExhibitionService) loadTags() (map[string]string, error) {
	rows, err := s.db.QueryContext(s.ctx, `
		SELECT game_id, name
		FROM game_tags
		ORDER BY game_id, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	builder := make(map[string][]string)
	for rows.Next() {
		var gameID, name string
		if err := rows.Scan(&gameID, &name); err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		builder[gameID] = append(builder[gameID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	joined := make(map[string]string, len(builder))
	for gameID, names := range builder {
		joined[gameID] = strings.Join(names, " / ")
	}
	return joined, nil
}

// normalizeExhibitionStatus 把状态收敛到页面认识的五个值。
//
// hall.js 按 playStatus 分区：playing / completed / onhold / dropped / 其余=unplayed。
// 空值与未知值一律归为 unplayed，保证「分区之和 == 库存总数」这条完备性校验成立。
func normalizeExhibitionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "playing", "completed", "onhold", "dropped":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unplayed"
	}
}

// firstAlias 取第一个「与主标题不同」的别名当作 originalTitle。
//
// 手机版有独立的 originalTitle 列（原名），桌面端没有 —— 别名列表是目前
// 最接近的信息。取不到就返回空串，页面会自己隐藏那一行副标题。
func firstAlias(aliasesJSON, title string) string {
	if strings.TrimSpace(aliasesJSON) == "" {
		return ""
	}
	var aliases []string
	if err := json.Unmarshal([]byte(aliasesJSON), &aliases); err != nil {
		return ""
	}
	trimmedTitle := strings.TrimSpace(title)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == trimmedTitle {
			continue
		}
		return alias
	}
	return ""
}

// normalizeExhibitionCoverURL 把库里的封面地址换成展厅页面能直接 img.src 的同源 URL。
//
// 页面跑在同一个 asset server 的同源 iframe 里，所以：
//   - /local/...          —— 本地封面，原样用（由 LocalFileHandler 直出文件）
//   - http(s)://...       —— 远端封面，走 /proxy/image（国内直连 vndb/bgm 常常不通，
//     与前端 ProxyImage 用的是同一条通路）
//   - http://wails.localhost/... —— 打包后的历史数据，去掉 host 免得混用协议
//   - 其它（空值、裸文件路径等）—— 视为没有封面，让页面画程序化占位
func normalizeExhibitionCoverURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/local/") {
		return trimmed
	}

	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}

	if parsed, err := url.Parse(trimmed); err == nil && strings.EqualFold(parsed.Hostname(), "wails.localhost") {
		if parsed.Path != "" {
			return parsed.Path
		}
		return trimmed
	}

	// 与前端 URLSearchParams 一致（空格编成 +，Go 侧 r.URL.Query() 能解回来）
	return "/proxy/image?" + url.Values{"url": {trimmed}}.Encode()
}
