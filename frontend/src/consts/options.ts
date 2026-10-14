import { enums } from "../../src/bindings/models";

export type GameStatusFilter = enums.GameStatus | "";

/**
 * 隐藏游戏的过滤档位（对齐手机版筛选栏的「隐藏」分类）。
 *
 * - `hide`：隐藏的游戏不进列表（默认，对齐手机版 `GameRepository.getAll` 的 `hidden=0`）
 * - `only`：**只看已隐藏的游戏** —— 就是手机版筛选栏里那项「隐藏」分类，
 *   也是「误隐藏了想找回来」的唯一入口
 *
 * 之所以只有两档：手机版的分类行本身就是**单选**（点了「隐藏」就离开状态筛选），
 * 不存在「既看正常游戏又混着看已隐藏」这一档。
 */
export type HiddenFilter = "hide" | "only";

export const DEFAULT_HIDDEN_FILTER: HiddenFilter = "hide";

export const statusOptions: Array<{ label: string; value: GameStatusFilter }>
  = [
    { label: "common.allStatus", value: "" },
    { label: "common.unplayed", value: enums.GameStatus.StatusUnplayed },
    { label: "common.playing", value: enums.GameStatus.StatusPlaying },
    { label: "common.completed", value: enums.GameStatus.StatusCompleted },
    { label: "common.onHold", value: enums.GameStatus.StatusOnHold },
    { label: "common.dropped", value: enums.GameStatus.StatusDropped },
  ];

export const sortOptions: Array<{
  label: string;
  value: enums.GameListSortBy;
}> = [
  { label: "common.name", value: enums.GameListSortBy.GameListSortByName },
  {
    label: "common.company",
    value: enums.GameListSortBy.GameListSortByCompany,
  },
  {
    label: "common.lastPlayedAt",
    value: enums.GameListSortBy.GameListSortByLastPlayedAt,
  },
  {
    label: "common.createdAt",
    value: enums.GameListSortBy.GameListSortByCreatedAt,
  },
  { label: "common.rating", value: enums.GameListSortBy.GameListSortByRating },
  {
    label: "common.releaseDate",
    value: enums.GameListSortBy.GameListSortByReleaseDate,
  },
  {
    label: "common.playTime",
    value: enums.GameListSortBy.GameListSortByPlayTime,
  },
];

export const APP_ZOOM_LEVELS = [0.8, 0.9, 1, 1.1, 1.25, 1.5] as const;
export const DEFAULT_APP_ZOOM = 1;
type AppZoomLevel = (typeof APP_ZOOM_LEVELS)[number];

export const appZoomOptions = APP_ZOOM_LEVELS.map(value => ({
  label: `${Math.round(value * 100)}%`,
  value: String(value),
}));

export const languageOptions = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁體中文" },
  { value: "en-US", label: "English" },
  { value: "ja-JP", label: "日本語" },
];

export function normalizeAppZoomFactor(value?: number) {
  if (typeof value !== "number" || Number.isNaN(value) || value <= 0) {
    return DEFAULT_APP_ZOOM;
  }

  let nearest: AppZoomLevel = APP_ZOOM_LEVELS[0];
  let nearestDistance = Math.abs(value - nearest);

  for (const zoomLevel of APP_ZOOM_LEVELS) {
    const distance = Math.abs(value - zoomLevel);
    if (distance < nearestDistance) {
      nearest = zoomLevel;
      nearestDistance = distance;
    }
  }

  return nearest;
}

export function getNextAppZoomFactor(current: number, direction: 1 | -1) {
  const normalized = normalizeAppZoomFactor(current);
  const currentIndex = APP_ZOOM_LEVELS.findIndex(
    level => level === normalized,
  );
  const nextIndex = Math.min(
    APP_ZOOM_LEVELS.length - 1,
    Math.max(0, currentIndex + direction),
  );
  return APP_ZOOM_LEVELS[nextIndex];
}
