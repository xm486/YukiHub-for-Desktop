import type { models } from "../../src/bindings/models";
import type { BigScreenHintMode } from "./BigScreenHintBar";
import type { BigScreenKeyStyle } from "./keyStyles";
import type { BigScreenInputDevice } from "./useGamepad";
import { memo } from "react";

import { useTranslation } from "react-i18next";
import { statusOptions } from "../consts/options";
import { useGamePlaytime } from "../hooks/useGamePlaytime";
import { useAppStore } from "../store";
import { getTagDisplayName } from "../utils/tagTranslation";
import { formatDurationCompact, formatLocalDate } from "../utils/time";
import { BigScreenHintBar } from "./BigScreenHintBar";

export interface BigScreenDetailAction {
  icon: string;
  key: string;
  label: string;
  run: () => void;
  /**
   * 保留给"条件不满足但需要占位"的条目；当前调用方对「观看 PV」改为整条隐藏
   * （手机端 M15 的做法），所以正常路径上不会出现禁用按钮。
   */
  disabled?: boolean;
}

interface BigScreenDetailsLayerProps {
  actions: BigScreenDetailAction[];
  actionsFocused: boolean;
  focusedActionIndex: number;
  game: models.Game;
  /** 提示条模式（auto / always / off） */
  hintMode?: BigScreenHintMode;
  /** 最近一次使用的输入设备，决定底部提示显示手柄图标还是键盘按键 */
  inputDevice: BigScreenInputDevice;
  /** 按键图标风格（Xbox / PlayStation） */
  keyStyle?: BigScreenKeyStyle;
  onActionActivate: (index: number) => void;
  onActionFocus: (index: number) => void;
  /** 关闭详情层回到货架 */
  onClose: () => void;
  tags: models.GameTag[];
}

/**
 * 大屏详情层，对齐手机端 `BigScreenDetailsLayer`：
 * 封面 / 标题 / 副行（原文名·开发商·发行日期）/ 标签 chips（≤3）/ 统计块 / 简介 / 操作按钮排。
 *
 * 操作按钮排由调用方给出，对齐手机端的「游玩 / 观看 PV（有才显示）/ 详细」——
 * 其中「详细」打开的是**大屏内的游戏操作菜单**，不是跳去游戏详情页（手机端
 * `onRequestGameMenu`），所以详情层本身不知道菜单长什么样。
 */
export const BigScreenDetailsLayer = memo(
  ({
    actions,
    actionsFocused,
    focusedActionIndex,
    game,
    hintMode = "auto",
    inputDevice,
    keyStyle = "xbox",
    onActionActivate,
    onActionFocus,
    onClose,
    tags,
  }: BigScreenDetailsLayerProps) => {
    const { t } = useTranslation();
    const enableTagTranslation = useAppStore(
      state => state.config?.enable_tag_translation ?? true,
    );
    const playTime = useGamePlaytime(game.id);

    const originalTitle
      = game.aliases?.find(alias => alias.trim().length > 0) ?? "";
    const statusLabel = statusOptions.find(
      option => option.value === game.status,
    )?.label;
    const visibleTags = tags.slice(0, 3);

    // 统计块（对齐手机端 M9/M10 的重排）：时长 / 上次游玩 / 状态恒有，
    // 「评分」只在真有数据时补一格 —— 一排"暂无"的方块比不显示更难看。
    const stats = [
      {
        label: t("bigScreen.playTime"),
        value: formatDurationCompact(playTime, t),
      },
      {
        label: t("common.lastPlayedAt"),
        value: game.last_played_at
          ? formatLocalDate(game.last_played_at)
          : t("common.never"),
      },
      {
        label: t("common.status"),
        value: statusLabel ? t(statusLabel) : t("common.unplayed"),
      },
    ];
    if (game.rating > 0) {
      stats.push({
        label: t("common.rating"),
        value: game.rating.toFixed(1),
      });
    }

    const coverUrl = game.cover_url || game.cover_source_url;

    return (
      <div
        className="absolute inset-0 z-30 flex items-center justify-center bg-brand-950/85 px-16 py-10 backdrop-blur-md"
        onClick={onClose}
      >
        <div
          className="grid w-full max-w-5xl grid-cols-[minmax(0,260px)_minmax(0,1fr)] items-start gap-8"
          onClick={event => event.stopPropagation()}
        >
          <div className="relative aspect-[3/3.6] overflow-hidden rounded-2xl bg-brand-800 shadow-2xl">
            {coverUrl && (
              <img
                className="h-full w-full object-cover"
                src={coverUrl}
                alt={game.name}
              />
            )}
            {game.is_nsfw && (
              <span className="absolute right-3 top-3 rounded bg-rose-600/90 px-2 py-0.5 text-xs font-bold text-white">
                R18
              </span>
            )}
          </div>

          <div className="flex max-h-[70vh] min-h-0 flex-col">
            <h2 className="text-4xl font-bold leading-tight text-white drop-shadow-[0_2px_12px_rgba(0,0,0,0.55)]">
              {game.name}
            </h2>

            <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-brand-400">
              {originalTitle && (
                <span className="truncate">{originalTitle}</span>
              )}
              {game.company && (
                <>
                  <span aria-hidden="true">·</span>
                  <span className="truncate">{game.company}</span>
                </>
              )}
              {game.release_date && (
                <>
                  <span aria-hidden="true">·</span>
                  <span>{game.release_date}</span>
                </>
              )}
            </div>

            {visibleTags.length > 0 && (
              <div className="mt-4 flex flex-wrap items-center gap-2">
                {visibleTags.map(tag => (
                  <span key={tag.id} className="yh-chip text-[11px]">
                    {getTagDisplayName(tag.name, enableTagTranslation)}
                  </span>
                ))}
              </div>
            )}

            <div
              className="mt-5 grid gap-3"
              style={{
                gridTemplateColumns: `repeat(${stats.length}, minmax(0, 1fr))`,
              }}
            >
              {stats.map(item => (
                <div
                  key={item.label}
                  className="rounded-xl border border-white/10 bg-white/5 px-4 py-3"
                >
                  <div className="text-xs text-brand-400">{item.label}</div>
                  <div className="mt-1 truncate text-lg font-semibold text-white">
                    {item.value}
                  </div>
                </div>
              ))}
            </div>

            {/* 简介为空时整块收起（对齐手机端 M10），不留一块「暂无简介」的空框 */}
            {game.summary && (
              <div
                data-bigscreen-details-scroll
                className="mt-6 min-h-0 flex-1 overflow-y-auto pr-2 text-sm leading-relaxed text-brand-300"
              >
                {game.summary}
              </div>
            )}

            <div className="mt-8 flex shrink-0 flex-wrap items-center gap-3">
              {actions.map((action, index) => {
                const isFocused
                  = actionsFocused && index === focusedActionIndex;

                return (
                  <button
                    key={action.key}
                    type="button"
                    aria-label={action.label}
                    disabled={action.disabled}
                    title={
                      action.disabled
                        ? t("bigScreen.trailerUnavailable")
                        : undefined
                    }
                    className={`inline-flex items-center gap-2 rounded-full border px-6 py-3 text-sm font-medium transition-all duration-150 ${
                      action.disabled
                        ? "cursor-not-allowed border-brand-800 bg-brand-800/40 text-brand-600"
                        : isFocused
                          ? "scale-105 border-secondary-500 bg-brand-750 text-white"
                          : "border-brand-700 bg-brand-800/70 text-brand-400 hover:border-brand-600 hover:text-white"
                    }`}
                    onClick={() => onActionActivate(index)}
                    onMouseEnter={() => onActionFocus(index)}
                  >
                    <span
                      className={`${action.icon} text-lg`}
                      aria-hidden="true"
                    />
                    {action.label}
                  </button>
                );
              })}
            </div>

            <BigScreenHintBar
              className="mt-4 shrink-0"
              hintMode={hintMode}
              inputDevice={inputDevice}
              keyStyle={keyStyle}
              variant="details"
            />
          </div>
        </div>
      </div>
    );
  },
);
