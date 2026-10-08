import type { models } from "../../../src/bindings/models";
import { useEffect, useState } from "react";
import toast from "react-hot-toast";
import { useTranslation } from "react-i18next";
import {
  GetGameMetadataSources,
  SetDefaultMetadataSource,
} from "../../../bindings/yukihub/internal/service/gameservice";
import { GAME_STATUS_BADGE_STYLES } from "../../consts/gameStatusBadge";
import { useGamePlaytime } from "../../hooks/useGamePlaytime";
import { getMetadataSourceIcon } from "../../utils/metadataSources";
import { formatDuration, formatLocalDate } from "../../utils/time";
import { BetterDropdownMenu } from "../ui/better/BetterDropdownMenu";
import { GameCoverImage } from "../ui/GameCoverImage";
import { GameScreenshots } from "../ui/GameScreenshots";
import { sourceLabel } from "../ui/import/importFlow";

interface LibraryDetailPanelProps {
  /** 未选中游戏时为 null，面板显示占位（面板常驻，避免网格宽度变化） */
  game: models.Game | null;
  isRunning: boolean;
  /** 切换该游戏的资料源后回调，由父级刷新列表（对齐手机版的「重新匹配」） */
  onMetadataSourceChanged?: () => void;
  onOpenDetail: (gameId: string) => void;
  onStart: (game: models.Game) => void;
}

/**
 * PC 版游戏库右侧详情面板，对齐手机版 `activity_main.xml` 的 `detailPanel`：
 * 固定宽的**紧凑信息列**，封面是一条横向横幅（手机版是 82dp 高的 centerCrop），
 * 按钮紧跟标题之后，简介与元信息依次排在下面。
 *
 * 刻意不做成「竖版大海报 + 按钮贴底」：那样面板高度会超过视口，
 * 按钮被挤到看不见的地方。
 */
export function LibraryDetailPanel({
  game,
  isRunning,
  onMetadataSourceChanged,
  onOpenDetail,
  onStart,
}: LibraryDetailPanelProps) {
  const { t } = useTranslation();
  // 列表请求已带批量时长并预填缓存；缓存未命中（例如命中本地列表缓存）时才单查
  const playTime = useGamePlaytime(game?.id);
  const [switchingSource, setSwitchingSource] = useState(false);
  // 列表行不带 metadata_sources（那是单条查询才补的），选中后单独取一次
  const [metadataSources, setMetadataSources] = useState<
    models.GameMetadataSource[]
  >([]);
  const [metadataSourceRevision, setMetadataSourceRevision] = useState(0);
  // 切换后先按用户的选择显示，等列表刷新到最新值再让位
  const [optimisticSource, setOptimisticSource] = useState<{
    gameId: string;
    source: string;
  } | null>(null);
  const gameId = game?.id ?? "";

  useEffect(() => {
    if (!gameId) {
      setMetadataSources([]);
      return;
    }
    let cancelled = false;
    void GetGameMetadataSources(gameId)
      .then((sources) => {
        if (!cancelled) {
          setMetadataSources(sources);
        }
      })
      .catch((error) => {
        console.error("Failed to load game metadata sources:", error);
        if (!cancelled) {
          setMetadataSources([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [gameId, metadataSourceRevision]);

  // 空态：对齐手机版面板里的「选择游戏」占位
  if (!game) {
    return (
      <section className="yh-glass flex min-h-[420px] flex-col items-center justify-center gap-2 p-4 text-center">
        <span
          className="i-mdi-cursor-default-click-outline text-3xl text-brand-400 dark:text-white/40"
          aria-hidden="true"
        />
        <p className="text-sm font-semibold text-brand-700 dark:text-white/80">
          {t("library.detailPlaceholder")}
        </p>
      </section>
    );
  }

  const coverSrc = game.cover_url || game.cover_source_url || "";
  const statusBadge = GAME_STATUS_BADGE_STYLES[game.status];

  /*
    该游戏当前使用的资料源：后端把它落在 games.source_type 上。
    刚切换完的那一刻列表可能还没重新加载回来，所以留一个「乐观值」：
    它只在和 prop 不一致时生效，等列表刷新到最新值就自动让位（无需 effect 清理）。
  */
  const currentMetadataSource
    = optimisticSource
      && optimisticSource.gameId === gameId
      && optimisticSource.source !== game.source_type
      ? (optimisticSource.source as models.Game["source_type"])
      : game.source_type;
  // 这个游戏缓存过哪些来源：来源多于一个时才能切换
  const availableMetadataSources = metadataSources
    .map(source => source.source_type)
    .filter(
      (source, index, list) =>
        Boolean(source) && list.indexOf(source) === index,
    );

  const handleSwitchMetadataSource = async (
    source: models.GameMetadataSource["source_type"],
  ) => {
    if (source === currentMetadataSource || switchingSource) {
      return;
    }
    setSwitchingSource(true);
    try {
      await SetDefaultMetadataSource(game.id, source);
      toast.success(
        t("library.detailSourceSwitched", { source: sourceLabel(source, t) }),
      );
      setOptimisticSource({ gameId, source });
      setMetadataSourceRevision(revision => revision + 1);
      onMetadataSourceChanged?.();
    }
    catch (error) {
      console.error("Failed to switch metadata source:", error);
      toast.error(t("library.detailSourceSwitchFailed"));
    }
    finally {
      setSwitchingSource(false);
    }
  };

  const infoRows = [
    {
      icon: "i-mdi-timer-outline",
      label: t("common.playTime"),
      value: playTime > 0 ? formatDuration(playTime, t) : t("common.never"),
    },
    {
      icon: "i-mdi-star-outline",
      label: t("common.rating"),
      value: game.rating > 0 ? `${game.rating.toFixed(1)}/10.0` : "--",
    },
    {
      icon: "i-mdi-history",
      label: t("common.lastPlayedAt"),
      value: game.last_played_at
        ? formatLocalDate(game.last_played_at)
        : t("common.never"),
    },
  ];

  return (
    <section className="yh-glass flex max-h-[calc(100vh-8rem)] flex-col gap-3 overflow-y-auto p-4">
      <header className="flex items-center gap-2">
        <h2 className="min-w-0 flex-1 truncate text-sm font-bold text-brand-900 dark:text-white">
          {t("library.detailTitle")}
        </h2>
      </header>

      {/* 横向封面横幅（手机版 detailPanel 顶部就是一条 82dp 高的 centerCrop 图） */}
      <div className="relative aspect-[16/7] w-full shrink-0 overflow-hidden rounded-xl border border-primary-200/70 bg-brand-200 dark:border-primary-300/30 dark:bg-brand-900/60">
        {coverSrc ? (
          <GameCoverImage
            src={coverSrc}
            fallbackSrc={game.cover_source_url}
            alt={game.name}
            isNSFW={game.is_nsfw}
            revealNSFWOnHover
            className="h-full w-full"
            imageClassName="h-full w-full object-cover object-center"
          />
        ) : (
          <div className="flex h-full items-center justify-center text-brand-400 dark:text-white/40">
            <span className="i-mdi-image-off text-2xl" aria-hidden="true" />
          </div>
        )}

        {statusBadge && (
          <span
            className={`absolute right-2 top-2 inline-flex items-center gap-1 rounded-full border border-white/25 px-2 py-0.5 text-[10px] font-bold leading-none text-white shadow-sm backdrop-blur-sm ${statusBadge.className}`}
          >
            <span
              className={`${statusBadge.icon} text-xs`}
              aria-hidden="true"
            />
            {t(statusBadge.labelKey)}
          </span>
        )}
      </div>

      <div className="min-w-0 shrink-0">
        <h3 className="truncate text-base font-bold text-brand-900 dark:text-white">
          {game.name}
        </h3>
        <p className="mt-0.5 truncate text-xs text-brand-600 dark:text-white/70">
          {game.company || t("common.unknownDeveloper")}
          {game.release_date ? ` · ${game.release_date}` : ""}
        </p>
      </div>

      {/*
        当前资料源（对齐手机版 detailPanel 顶部的来源徽标）：
        显示这个游戏现在用的是哪个源，来源多于一个时点开即可切换
        —— 就是手机版那颗「重新匹配 <源>」。
      */}
      <div className="flex min-w-0 shrink-0 items-center gap-2">
        <span
          className="i-mdi-database-search shrink-0 text-sm text-primary-600 dark:text-primary-300"
          aria-hidden="true"
        />
        <span className="shrink-0 text-[11px] text-brand-500 dark:text-white/60">
          {t("library.detailSource")}
        </span>
        {availableMetadataSources.length === 0 ? (
          <span className="min-w-0 flex-1 truncate text-right text-xs text-brand-400 dark:text-white/45">
            {t("library.detailSourceNone")}
          </span>
        ) : (
          <BetterDropdownMenu
            align="end"
            ariaLabel={t("library.detailSourceSwitch")}
            menuWidth="min-w-[230px]"
            title={t("library.detailSourceSwitch")}
            disabled={availableMetadataSources.length <= 1 || switchingSource}
            items={availableMetadataSources.map(source => ({
              key: source,
              label: sourceLabel(source, t),
              description:
                source === currentMetadataSource
                  ? t("library.detailSourceCurrent")
                  : t("library.detailSourceOther"),
              iconSrc: getMetadataSourceIcon(source, "compact") ?? undefined,
              onClick: () => void handleSwitchMetadataSource(source),
            }))}
            trigger={(
              <button
                type="button"
                disabled={
                  availableMetadataSources.length <= 1 || switchingSource
                }
                className="ml-auto inline-flex max-w-full items-center gap-1 rounded-full border border-primary-200/70 bg-white/70 px-2 py-0.5 text-[11px] font-semibold text-brand-700 transition-colors hover:bg-white disabled:cursor-default disabled:hover:bg-white/70 dark:border-primary-300/40 dark:bg-[#1D2B3E]/70 dark:text-white/90 dark:hover:bg-[#1D2B3E] dark:disabled:hover:bg-[#1D2B3E]/70"
              >
                {getMetadataSourceIcon(currentMetadataSource, "compact") && (
                  <img
                    src={getMetadataSourceIcon(
                      currentMetadataSource,
                      "compact",
                    )}
                    alt=""
                    className="h-3.5 w-3.5 shrink-0 object-contain"
                  />
                )}
                <span className="truncate">
                  {sourceLabel(currentMetadataSource, t)
                    || t("library.detailSourceNone")}
                </span>
                {availableMetadataSources.length > 1 && (
                  <span
                    className="i-mdi-chevron-down shrink-0 text-xs"
                    aria-hidden="true"
                  />
                )}
              </button>
            )}
          />
        )}
      </div>

      {/* 按钮紧跟标题（对齐手机版：标题 → 按钮 → 简介 → 元信息），不再贴底 */}
      <div className="flex shrink-0 gap-2">
        <button
          type="button"
          onClick={() => onStart(game)}
          disabled={isRunning}
          className="yh-primary-pill inline-flex h-9 flex-1 items-center justify-center gap-1.5 rounded-full text-xs font-bold text-white shadow-md transition-all hover:brightness-110 active:scale-95 disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:brightness-100"
        >
          <span
            className={
              isRunning
                ? "i-mdi-gamepad-variant text-base"
                : "i-mdi-play text-base"
            }
            aria-hidden="true"
          />
          {isRunning ? t("common.playing") : t("gameCard.startGame")}
        </button>
        <button
          type="button"
          onClick={() => onOpenDetail(game.id)}
          className="inline-flex h-9 flex-1 items-center justify-center gap-1.5 rounded-full border border-primary-200/70 bg-white/70 text-xs font-semibold text-brand-700 transition-colors hover:bg-white dark:border-primary-300/40 dark:bg-[#1D2B3E]/70 dark:text-white/90 dark:hover:bg-[#1D2B3E]"
        >
          <span className="i-mdi-open-in-new text-base" aria-hidden="true" />
          {t("library.viewFullDetail")}
        </button>
      </div>

      <div className="min-w-0 shrink-0">
        <p className="text-[11px] font-semibold text-brand-500 dark:text-white/60">
          {t("game.summary")}
        </p>
        <p className="mt-1 line-clamp-4 text-xs leading-relaxed text-brand-600 dark:text-white/70">
          {game.summary || t("game.noSummary")}
        </p>
      </div>

      <dl className="flex shrink-0 flex-col gap-1.5">
        {infoRows.map(row => (
          <div key={row.label} className="flex items-center gap-2">
            <span
              className={`${row.icon} shrink-0 text-sm text-primary-600 dark:text-primary-300`}
              aria-hidden="true"
            />
            <dt className="shrink-0 text-[11px] text-brand-500 dark:text-white/60">
              {row.label}
            </dt>
            <dd className="min-w-0 flex-1 truncate text-right text-xs font-medium text-brand-800 dark:text-white/90">
              {row.value}
            </dd>
          </div>
        ))}
      </dl>

      {/* 媒体：截图（对齐手机端 detailPanel 的「媒体」小节，2 个固定位） */}
      <GameScreenshots
        gameId={game.id}
        isNSFW={game.is_nsfw}
        limit={2}
        label={t("game.media")}
      />
    </section>
  );
}
