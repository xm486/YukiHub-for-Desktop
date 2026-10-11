import type { models } from "../../src/bindings/models";
import type { BigScreenBannerMessage } from "../bigscreen/BigScreenBanner";
import type { BigScreenDetailAction } from "../bigscreen/BigScreenDetailsLayer";
import type { BigScreenHintMode } from "../bigscreen/BigScreenHintBar";
import type { BigScreenAction } from "../bigscreen/BigScreenInfoBar";
import type { BigScreenIntroHandle } from "../bigscreen/BigScreenIntro";
import type { BigScreenPanelItem } from "../bigscreen/BigScreenPanel";
import type { BigScreenSettingsLayerHandle } from "../bigscreen/BigScreenSettingsLayer";
import type { BigScreenSound } from "../bigscreen/bigScreenSound";
import type {
  BigScreenCategoryId,
  BigScreenSortMode,
} from "../bigscreen/categories";
import type { FocusDirection, FocusZone } from "../bigscreen/focusEngine";
import type { BigScreenKeyStyle } from "../bigscreen/keyStyles";
import type {
  BigScreenInputDevice,
  BigScreenIntent,
} from "../bigscreen/useGamepad";
import { createRoute, useNavigate } from "@tanstack/react-router";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { toast } from "react-hot-toast";
import { useTranslation } from "react-i18next";
import {
  AddGameToCategory,
  GetCategoryGames,
  RemoveGameFromCategory,
} from "../../bindings/yukihub/internal/service/categoryservice";
import {
  ClearBigScreenIntroVideo,
  SelectBigScreenIntroVideo,
} from "../../bindings/yukihub/internal/service/configservice";
import {
  BatchUpdateStatus,
  ClearGameArt,
  DeleteGame,
  GetGames,
  OpenLocalPath,
  RemoveGameTrailer,
  SelectGameArt,
  SelectGameTrailer,
  SetGameHidden,
} from "../../bindings/yukihub/internal/service/gameservice";
import { enums } from "../../src/bindings/models";
import { BigScreenAtmosphere } from "../bigscreen/BigScreenAtmosphere";
import { BigScreenBackground } from "../bigscreen/BigScreenBackground";
import { BigScreenBanner } from "../bigscreen/BigScreenBanner";
import { BigScreenDetailsLayer } from "../bigscreen/BigScreenDetailsLayer";
import { BigScreenHintBar } from "../bigscreen/BigScreenHintBar";
import { BigScreenInfoBar } from "../bigscreen/BigScreenInfoBar";
import { BigScreenIntro } from "../bigscreen/BigScreenIntro";
import {
  BigScreenPanel,
  firstFocusableIndex,
  focusedPanelItem,
  stepFocusableIndex,
} from "../bigscreen/BigScreenPanel";
import { BigScreenRail } from "../bigscreen/BigScreenRail";
import { BigScreenSettingsLayer } from "../bigscreen/BigScreenSettingsLayer";
import { playBigScreenSound } from "../bigscreen/bigScreenSound";
import { BigScreenTopBar } from "../bigscreen/BigScreenTopBar";
import { BigScreenTrailerPlayer } from "../bigscreen/BigScreenTrailerPlayer";
import {
  BIG_SCREEN_CATEGORIES,
  BIG_SCREEN_SORT_MODES,
  fetchBigScreenCategoryCounts,
  fetchBigScreenGames,
  resolveFavoritesCategoryId,
} from "../bigscreen/categories";
import {
  BIG_SCREEN_ACTIONS_ZONE,
  BIG_SCREEN_DEFAULT_CONFIG,
  BIG_SCREEN_DETAILS_ZONE,
  BIG_SCREEN_EXIT_PATH,
  BIG_SCREEN_FOCUS_ORDER,
  BIG_SCREEN_PAGE_LIMIT,
  BIG_SCREEN_RAIL_ZONE,
  BIG_SCREEN_SHELF_LIMIT,
  BIG_SCREEN_SHELF_ZONE,
  resolveBigScreenEffectLevel,
  resolveBigScreenShelfMetrics,
} from "../bigscreen/constants";
import {
  resolveBigScreenKeyGlyphs,
  resolveBigScreenKeyStyle,
} from "../bigscreen/keyStyles";
import { createBigScreenSettingSections } from "../bigscreen/settingsSchema";
import { useBigScreenFullscreen } from "../bigscreen/useBigScreenFullscreen";
import { useFocusEngine } from "../bigscreen/useFocusEngine";
import { useGameFavorite } from "../bigscreen/useGameFavorite";
import { useGamepad } from "../bigscreen/useGamepad";
import { useGameTags } from "../bigscreen/useGameTags";
import { useTrailerHover } from "../bigscreen/useTrailerHover";
import { VirtualGameShelf } from "../bigscreen/VirtualGameShelf";
import { invalidateAllGameLists } from "../cache/gameCache";
import { GAME_STATUS_BADGE_STYLES } from "../consts/gameStatusBadge";
import { statusOptions } from "../consts/options";
import { useAppStore } from "../store";
import { Route as rootRoute } from "./__root";

const KEY_DIRECTIONS: Record<string, FocusDirection> = {
  ArrowDown: "down",
  ArrowLeft: "left",
  ArrowRight: "right",
  ArrowUp: "up",
};

/** 侧栏分类数（侧栏是单列，行长度恒为 1） */
const CATEGORY_COUNT = BIG_SCREEN_CATEGORIES.length;

/** 信息浮层的操作条目数：启动 / 收藏 / 详情 / 更多 */
const ACTION_COUNT = 4;

/** 「隐藏游戏管理」一次最多列出多少条（够用即可，不与货架上限一致） */
const HIDDEN_GAMES_LIMIT = 240;

/**
 * 游玩状态选项：来自全库共用的 `statusOptions`，去掉「全部状态」那个空值项。
 *
 * 大屏以前只做三态循环（对齐手机端 `cycleStatus`），**「搁置 / 抛弃」在大屏里根本
 * 设不了**；用户明确要求能手动选，所以这里列出全部五档。
 */
const STATUS_CHOICES = statusOptions.filter(
  (option): option is { label: string; value: enums.GameStatus } =>
    option.value !== "",
);

/** 与 Go 侧默认值保持一致：config 还没加载出来时用它们兜底 */
const CONFIG_FALLBACK = {
  bannerHoldMs: 2000,
  cardScale: 112,
  focusScale: 100,
  pvScrimPercent: 45,
  soundVolume: 65,
  trailerDelayMs: 2000,
};

/** 面板种类：手机端 S4 / S5 / 隐藏管理 / 二次确认 / 设置选项选择器 */
type PanelKind
  = | "confirm-delete"
    | "confirm-hide"
    | "confirm-reset"
    | "game"
    | "game-status"
    | "hidden"
    | "main"
    | "setting-choices";

function normalizeCategoryId(value: string | undefined): BigScreenCategoryId {
  return BIG_SCREEN_CATEGORIES.some(category => category.id === value)
    ? (value as BigScreenCategoryId)
    : "recent";
}

/** 打开游戏目录：优先用 game_directory，退化到启动文件的上一级 */
function resolveGameDirectory(game: models.Game) {
  if (game.game_directory) {
    return game.game_directory;
  }
  return game.path.replace(/[\\/][^\\/]*$/, "");
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/bigscreen",
  component: BigScreenPage,
});

function BigScreenPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const startGame = useAppStore(state => state.startGame);
  const patchLiveConfig = useAppStore(state => state.patchLiveConfig);
  const config = useAppStore(state => state.config);

  // ===== 偏好（全部来自 appconfig 的 bigscreen_*，缺省值与 Go 侧一致） =====
  const showHiddenGame = config?.bigscreen_show_hidden_game ?? false;
  const effectLevel = resolveBigScreenEffectLevel(
    config?.bigscreen_effect_level,
  );
  const soundEnabled = config?.bigscreen_sound_enabled ?? true;
  const soundVolume
    = config?.bigscreen_sound_volume ?? CONFIG_FALLBACK.soundVolume;
  const focusTicks = config?.bigscreen_focus_ticks ?? true;
  const introEnabled = config?.bigscreen_intro_enabled ?? true;
  /** 自选入场视频（/local/intro/...）；空 = 内置动画 */
  const introVideo = config?.bigscreen_intro_video ?? "";
  const showTitles = config?.bigscreen_show_titles ?? false;
  const cardScale = config?.bigscreen_card_scale ?? CONFIG_FALLBACK.cardScale;
  const focusScale
    = config?.bigscreen_focus_scale ?? CONFIG_FALLBACK.focusScale;
  const keyStyle: BigScreenKeyStyle = resolveBigScreenKeyStyle(
    config?.bigscreen_key_style,
  );
  const hintMode: BigScreenHintMode = (() => {
    const raw = config?.bigscreen_hint_mode;
    return raw === "always" || raw === "off" ? raw : "auto";
  })();
  const railPinned = config?.bigscreen_rail_expanded ?? false;
  const trailerEnabled = config?.bigscreen_trailer_enabled ?? true;
  const trailerMuted = config?.bigscreen_trailer_muted ?? false;
  const trailerDelayMs
    = config?.bigscreen_trailer_delay_ms ?? CONFIG_FALLBACK.trailerDelayMs;
  const pvFit = config?.bigscreen_pv_fit ?? false;
  const pvScrim = config?.bigscreen_pv_scrim ?? true;
  const pvScrimPercent
    = config?.bigscreen_pv_scrim_percent ?? CONFIG_FALLBACK.pvScrimPercent;
  const bannerHoldMs
    = config?.bigscreen_banner_hold_ms ?? CONFIG_FALLBACK.bannerHoldMs;
  const snowEnabled = config?.bigscreen_snow_enabled ?? true;
  const trailerDetailsOnly = config?.bigscreen_trailer_details_only ?? false;
  const rememberFilter = config?.bigscreen_remember_filter ?? true;

  const defaults = useAppStore(
    state => state.config?.bigscreen_default_category,
  );
  /** 上次退出大屏时停留的分类（仅在「记住筛选」打开时写入） */
  const lastCategory = useAppStore(
    state => state.config?.bigscreen_last_category,
  );

  const [activeCategory, setActiveCategory] = useState<BigScreenCategoryId>(
    () => normalizeCategoryId(defaults),
  );
  const [sortMode, setSortMode] = useState<BigScreenSortMode>("recent");
  const [games, setGames] = useState<models.Game[]>([]);
  const [categoryCounts, setCategoryCounts] = useState<
    Partial<Record<BigScreenCategoryId, number>>
  >({});
  const [favoriteIds, setFavoriteIds] = useState<Set<string>>(() => new Set());
  const [hiddenGames, setHiddenGames] = useState<models.Game[]>([]);
  const [reloadToken, setReloadToken] = useState(0);
  const [shelfIndex, setShelfIndex] = useState(0);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [trailerOpen, setTrailerOpen] = useState(false);
  const [shelfAreaSize, setShelfAreaSize] = useState({ height: 0, width: 0 });
  const [railHovered, setRailHovered] = useState(false);
  const [hintKey, setHintKey] = useState(0);
  // null = 用户还没用过任何输入设备，此时按手柄是否接入决定提示条形态
  const [inputDevice, setInputDevice] = useState<BigScreenInputDevice | null>(
    null,
  );
  const [entryAnimation, setEntryAnimation] = useState(false);
  const [introPlaying, setIntroPlaying] = useState(introEnabled);
  /**
   * 主界面揭示（圆形扩散 + 1.06→1.0 回缩）：由入场动画在 1120ms 时通知，
   * 620ms 后自行摘掉。挂在内容层上而不是入场层里 —— 入场层盖着内容，
   * 没法给自己"下面"的元素做 clip-path。
   */
  const [contentRevealing, setContentRevealing] = useState(false);
  const introRef = useRef<BigScreenIntroHandle | null>(null);
  const [panelKind, setPanelKind] = useState<PanelKind | null>(null);
  const [panelGame, setPanelGame] = useState<models.Game | null>(null);
  const [panelIndex, setPanelIndex] = useState(0);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingTarget, setSettingTarget] = useState<{
    itemIndex: number;
    sectionIndex: number;
  } | null>(null);
  /** 顶部提示条：message.id 变化即视为新消息（动画重播） */
  const [banner, setBanner] = useState<BigScreenBannerMessage | null>(null);

  const shelfAreaRef = useRef<HTMLDivElement | null>(null);
  const restoredCategoryRef = useRef<BigScreenCategoryId | null>(null);
  /** 当前货架下标的实时镜像：切分类时要把它记进该分类的记忆里 */
  const shelfIndexRef = useRef(0);
  /**
   * 每个分类各自记住上次选中的卡片（对齐手机端 `FocusEngine.setMemoryKey` ——
   * 以筛选 id 为记忆键）。桌面端比手机端更依赖它：一屏能放好几张卡，
   * 来回切分类后又要从第一张重新找非常烦。
   */
  const shelfMemoryRef = useRef<Map<BigScreenCategoryId, number>>(new Map());
  // 「默认分类」只在配置真正可读之后应用一次，避免配置异步到达时被忽略
  const appliedDefaultCategoryRef = useRef(false);
  const settingsRef = useRef<BigScreenSettingsLayerHandle | null>(null);
  // 引擎在构造时就固定了 onBoundary，这里用 ref 把「之后才定义的处理函数」接进去
  const boundaryHandlerRef = useRef<
    (zoneId: string, direction: FocusDirection) => boolean
  >(() => false);

  useBigScreenFullscreen();

  /** 当前排序方式的显示文案（写成字面量 t() 调用，i18n 提取器才看得到） */
  const sortLabel
    = sortMode === "name"
      ? t("bigScreen.sortName")
      : sortMode === "newest"
        ? t("bigScreen.sortNewest")
        : t("bigScreen.sortRecent");

  // 入场错峰动画：等入场动画结束再播，避免"没看到就播完了"
  useEffect(() => {
    if (introPlaying) {
      // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
      setEntryAnimation(false);
      return;
    }
    // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
    setEntryAnimation(true);
    const timer = window.setTimeout(() => setEntryAnimation(false), 900);
    return () => window.clearTimeout(timer);
  }, [introPlaying]);

  /** 入场动画走到"揭示"阶段：给内容层挂上圆形扩散动画，动画跑完摘掉 */
  const handleIntroReveal = useCallback(() => {
    setContentRevealing(true);
    window.setTimeout(() => setContentRevealing(false), 700);
  }, []);

  const excludeHidden = !showHiddenGame;
  // 进入时决定初始分类：配置是异步读进来的，首帧可能还没有值，读到再应用一次。
  // 「记住筛选」打开时优先恢复上次的分类（对齐手机端 prefs.lastFilter），
  // 否则退回「默认分类」设置项。
  useEffect(() => {
    if (appliedDefaultCategoryRef.current) {
      return;
    }
    const target = (rememberFilter ? lastCategory : "") || defaults;
    if (!target) {
      return;
    }
    appliedDefaultCategoryRef.current = true;
    // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
    setActiveCategory(normalizeCategoryId(target));
  }, [defaults, lastCategory, rememberFilter]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const next = await fetchBigScreenGames(
          activeCategory,
          sortMode,
          excludeHidden,
        );
        if (active) {
          setGames(next);
        }
      }
      catch (error) {
        console.error("Failed to load big screen games:", error);
        if (active) {
          setGames([]);
        }
      }
    })();

    return () => {
      active = false;
    };
  }, [activeCategory, excludeHidden, reloadToken, sortMode]);

  // 侧栏计数与收藏 id 集合：只依赖「数据可能变了」的信号，不随焦点变化
  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const counts = await fetchBigScreenCategoryCounts(
          sortMode,
          excludeHidden,
        );
        if (active) {
          setCategoryCounts(counts);
        }
      }
      catch {
        // 计数失败不该影响浏览
      }
    })();
    return () => {
      active = false;
    };
  }, [excludeHidden, reloadToken, sortMode]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const favoritesId = await resolveFavoritesCategoryId();
        if (!favoritesId) {
          if (active) {
            setFavoriteIds(new Set());
          }
          return;
        }
        // 后端单次查询上限 240（超过会被静默夹取），所以按页取满
        const ids = new Set<string>();
        while (ids.size < BIG_SCREEN_SHELF_LIMIT) {
          const response = await GetCategoryGames({
            category_id: favoritesId,
            limit: BIG_SCREEN_PAGE_LIMIT,
            offset: ids.size,
            search_query: "",
            tags: [],
          } as never);
          const page = response.games ?? [];
          page.forEach(game => ids.add(game.id));
          if (!response.has_more || page.length === 0) {
            break;
          }
        }
        if (active) {
          setFavoriteIds(ids);
        }
      }
      catch {
        // 收藏角标拿不到就只是不显示，不影响主流程
      }
    })();
    return () => {
      active = false;
    };
  }, [reloadToken]);

  // 「隐藏游戏管理」需要显式列出隐藏项：后端没有「只看隐藏」的过滤，
  // 所以拉一份包含隐藏的列表再在本地筛（手机端也是内存里筛）。
  useEffect(() => {
    if (panelKind !== "hidden") {
      return;
    }
    let active = true;
    void (async () => {
      try {
        const response = await GetGames({
          limit: HIDDEN_GAMES_LIMIT,
          offset: 0,
          search_query: "",
          exclude_hidden: false,
          tags: [],
          sort_by: enums.GameListSortBy.GameListSortByLastPlayedAt,
          sort_order: enums.SortOrder.SortOrderDesc,
        } as never);
        if (active) {
          setHiddenGames((response.games ?? []).filter(game => game.hidden));
        }
      }
      catch (error) {
        console.error("Failed to load hidden games:", error);
      }
    })();
    return () => {
      active = false;
    };
  }, [panelKind, reloadToken]);

  useLayoutEffect(() => {
    const element = shelfAreaRef.current;
    if (!element) {
      return;
    }

    const updateSize = () => {
      const next = {
        height: element.clientHeight,
        // 内容层左右各 px-10，卡片可用宽度要扣掉
        width: Math.max(0, element.clientWidth - 80),
      };
      // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
      setShelfAreaSize(previous =>
        previous.height === next.height && previous.width === next.width
          ? previous
          : next,
      );
    };
    updateSize();

    const observer = new ResizeObserver(updateSize);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  // 尺寸预算统一由 constants 里的公式算（照搬手机端 BigScreenSizes）
  const shelfMetrics = useMemo(
    () =>
      resolveBigScreenShelfMetrics(shelfAreaSize, { cardScale, showTitles }),
    [cardScale, shelfAreaSize, showTitles],
  );

  const panelOpen = panelKind !== null;
  const overlayOpen = panelOpen || settingsOpen;

  const closePanel = useCallback(() => {
    setPanelKind(null);
    setPanelGame(null);
    setSettingTarget(null);
  }, []);

  const openPanel = useCallback((kind: PanelKind, game?: models.Game) => {
    setPanelGame(game ?? null);
    setPanelKind(kind);
    setPanelIndex(0);
  }, []);

  /** 顶部提示条的递增 id：每换一条消息都换一个，动画才会重播 */
  const bannerIdRef = useRef(0);
  const showBanner = useCallback((text: string, icon?: string) => {
    bannerIdRef.current += 1;
    // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
    setBanner({ icon, id: bannerIdRef.current, text });
  }, []);

  /** 清除「记住筛选」的残留：内存里的分类焦点记忆 + 配置里的上次分类 */
  const clearFilterMemory = useCallback(() => {
    shelfMemoryRef.current.clear();
    void patchLiveConfig({ bigscreen_last_category: "" });
    showBanner(t("bigScreen.filterMemoryCleared"));
  }, [patchLiveConfig, showBanner, t]);

  // ===== 入场动画来源（对齐手机端 M18-2：可选自己的视频当开场） =====

  /** 右列文案：内置动画 / 已选视频的文件名 */
  const introSourceLabel = useMemo(() => {
    if (!introVideo) {
      return t("bigScreen.introBuiltin");
    }
    const name = introVideo.split("/").filter(Boolean).pop() ?? "";
    return name || t("bigScreen.introBuiltin");
  }, [introVideo, t]);

  const pickIntroVideo = useCallback(() => {
    void (async () => {
      try {
        // Go 侧已把文件复制进受管目录并写进配置，这里同步 store 即可
        const path = await SelectBigScreenIntroVideo();
        if (!path) {
          return; // 用户取消
        }
        await patchLiveConfig({ bigscreen_intro_video: path });
        showBanner(t("bigScreen.introVideoSet"));
      }
      catch (error) {
        console.error("Failed to pick intro video:", error);
        showBanner(t("bigScreen.introVideoFailed"), "mdi:alert-circle-outline");
      }
    })();
  }, [patchLiveConfig, showBanner, t]);

  const clearIntroVideo = useCallback(() => {
    void (async () => {
      try {
        await ClearBigScreenIntroVideo();
        await patchLiveConfig({ bigscreen_intro_video: "" });
        showBanner(t("bigScreen.introVideoCleared"));
      }
      catch (error) {
        console.error("Failed to clear intro video:", error);
      }
    })();
  }, [patchLiveConfig, showBanner, t]);

  const activeCategoryIndex = BIG_SCREEN_CATEGORIES.findIndex(
    category => category.id === activeCategory,
  );
  const activeCategoryLabelKey
    = activeCategoryIndex >= 0
      ? BIG_SCREEN_CATEGORIES[activeCategoryIndex].labelKey
      : "";
  const activeCategoryLabel = t(activeCategoryLabelKey);

  // 设置项 schema 由设置页与大屏内面板共用；动作类条目把宿主回调注进去
  const settingsSections = useMemo(
    () =>
      createBigScreenSettingSections(t, {
        clearFilterMemory,
        // 只在真的选了视频时才出现「恢复内置动画」
        clearIntroVideo: introVideo ? clearIntroVideo : undefined,
        filterMemoryLabel: activeCategoryLabel,
        introSourceLabel,
        pickIntroVideo,
        // 恢复默认是破坏性操作，先走二次确认面板（对齐手机端的 AlertDialog）
        resetDefaults: () => openPanel("confirm-reset"),
      }),
    [
      activeCategoryLabel,
      clearFilterMemory,
      clearIntroVideo,
      introSourceLabel,
      introVideo,
      openPanel,
      pickIntroVideo,
      t,
    ],
  );

  const panelItems = useMemo<BigScreenPanelItem[]>(() => {
    switch (panelKind) {
      case "game": {
        const game = panelGame;
        if (!game) {
          return [];
        }
        // 五档都要认出名字：以前只认「游玩中 / 已通关」，搁置与抛弃被显示成「未玩」
        const statusLabelKey
          = STATUS_CHOICES.find(option => option.value === game.status)
            ?.label ?? "common.unplayed";
        const isFavorite = favoriteIds.has(game.id);
        return [
          {
            icon: "i-mdi-play",
            key: "start",
            label: t("gameCard.startGame"),
          },
          {
            icon: isFavorite ? "i-mdi-heart" : "i-mdi-heart-outline",
            key: "favorite",
            label: isFavorite
              ? t("bigScreen.unfavorite")
              : t("bigScreen.favorite"),
          },
          {
            icon: "i-mdi-check-circle-outline",
            key: "status",
            label: t("bigScreen.menuStatus", { status: t(statusLabelKey) }),
            // 说明它打开的是选择列表，而不是「点一次换一档」
            sub: t("bigScreen.statusPickHint"),
          },
          // 自定义标题图/背景图（对齐手机端 M10：Steam 式 logo + 自定义背景）
          ...(game.logo_path
            ? [
                {
                  icon: "i-mdi-image-outline",
                  key: "logo-set",
                  label: t("bigScreen.changeLogoArt"),
                  sub: t("bigScreen.artSetHint"),
                },
                {
                  icon: "i-mdi-image-off-outline",
                  key: "logo-remove",
                  label: t("bigScreen.clearLogoArt"),
                },
              ]
            : [
                {
                  icon: "i-mdi-image-outline",
                  key: "logo-set",
                  label: t("bigScreen.setLogoArt"),
                  sub: t("bigScreen.logoArtHint"),
                },
              ]),
          ...(game.bg_path
            ? [
                {
                  icon: "i-mdi-wallpaper",
                  key: "bg-set",
                  label: t("bigScreen.changeBgArt"),
                  sub: t("bigScreen.artSetHint"),
                },
                {
                  icon: "i-mdi-close-circle-outline",
                  key: "bg-remove",
                  label: t("bigScreen.clearBgArt"),
                },
              ]
            : [
                {
                  icon: "i-mdi-wallpaper",
                  key: "bg-set",
                  label: t("bigScreen.setBgArt"),
                  sub: t("bigScreen.bgArtHint"),
                },
              ]),
          {
            icon: "i-mdi-movie-open-outline",
            key: "trailer-set",
            label: game.trailer_path
              ? t("bigScreen.changeTrailer")
              : t("bigScreen.setTrailer"),
          },
          ...(game.trailer_path
            ? [
                {
                  icon: "i-mdi-movie-off-outline",
                  key: "trailer-remove",
                  label: t("bigScreen.removeTrailer"),
                },
              ]
            : []),
          {
            icon: "i-mdi-pencil-outline",
            key: "edit",
            label: t("bigScreen.menuEdit"),
            sub: t("bigScreen.menuEditHint"),
          },
          {
            icon: "i-mdi-folder-open-outline",
            key: "open-dir",
            label: t("bigScreen.menuOpenDirectory"),
          },
          { key: "sep-1", label: "", separator: true },
          {
            icon: game.hidden ? "i-mdi-eye-outline" : "i-mdi-eye-off-outline",
            key: "hide",
            label: game.hidden
              ? t("bigScreen.unhideGame")
              : t("bigScreen.hideGame"),
            sub: t("bigScreen.hideGameHint"),
          },
          {
            icon: "i-mdi-delete-outline",
            key: "delete",
            label: t("bigScreen.menuDelete"),
            sub: t("bigScreen.menuDeleteHint"),
          },
        ];
      }
      // 游玩状态选择：五档一次列全，当前档打勾（写法对齐设置项的 choice 面板）。
      // 以前是「点一次循环一档」，且只循环三档 —— 搁置 / 抛弃在大屏里根本设不了，
      // 用户明确要求能手动选。
      case "game-status": {
        const game = panelGame;
        if (!game) {
          return [];
        }
        return STATUS_CHOICES.map((option) => {
          const selected = option.value === game.status;
          return {
            icon:
              GAME_STATUS_BADGE_STYLES[option.value]?.icon
              ?? "i-mdi-check-circle-outline",
            key: `status:${option.value}`,
            label: `${selected ? "✓ " : "　"}${t(option.label)}`,
          };
        });
      }
      case "main": {
        return [
          {
            icon: "i-mdi-cog-outline",
            key: "settings",
            label: t("bigScreen.settingsTitle"),
          },
          {
            icon: "i-mdi-eye-off-outline",
            key: "hidden",
            label: t("bigScreen.hiddenGames"),
            sub: t("bigScreen.hiddenGamesHint"),
          },
          {
            icon: "i-mdi-shuffle-variant",
            key: "random",
            label: t("bigScreen.randomGame"),
          },
          {
            icon: "i-mdi-sort",
            key: "sort",
            label: t("bigScreen.switchSort"),
            sub: sortLabel,
          },
          {
            icon: "i-mdi-keyboard-outline",
            key: "shortcuts",
            label: t("bigScreen.shortcuts"),
          },
          { key: "sep-main", label: "", separator: true },
          {
            icon: "i-mdi-exit-to-app",
            key: "exit",
            label: t("bigScreen.exitMode"),
          },
        ];
      }
      case "hidden": {
        return [
          {
            icon: "i-mdi-arrow-left",
            key: "back",
            label: t("bigScreen.backToMainMenu"),
          },
          { key: "sep-hidden", label: "", separator: true },
          ...hiddenGames.map(game => ({
            icon: "i-mdi-eye-outline",
            key: `hidden:${game.id}`,
            label: game.name,
            sub: t("bigScreen.hiddenGameRestoreHint"),
          })),
        ];
      }
      case "confirm-hide": {
        return [
          { key: "cancel", label: t("common.cancel") },
          {
            icon: "i-mdi-eye-off-outline",
            key: "confirm",
            label: t("bigScreen.confirmHideGame"),
            sub: t("bigScreen.hideGameHint"),
          },
        ];
      }
      case "confirm-delete": {
        return [
          { key: "cancel", label: t("common.cancel") },
          {
            icon: "i-mdi-delete-outline",
            key: "confirm",
            label: t("bigScreen.confirmDeleteGame"),
            sub: t("bigScreen.menuDeleteHint"),
          },
        ];
      }
      case "confirm-reset": {
        return [
          { key: "cancel", label: t("common.cancel") },
          {
            icon: "i-mdi-restore",
            key: "confirm",
            label: t("bigScreen.confirmResetDefaults"),
            sub: t("bigScreen.resetDefaultsHint"),
          },
        ];
      }
      case "setting-choices": {
        const setting = settingTarget
          ? settingsSections[settingTarget.sectionIndex]?.settings[
            settingTarget.itemIndex
          ]
          : undefined;
        if (!setting?.choices || !config) {
          return [];
        }
        const current = setting.read?.(config) ?? "";
        return setting.choices.map(choice => ({
          key: `choice:${choice.value}`,
          label: `${choice.value === current ? "✓ " : "　"}${choice.label}`,
        }));
      }
      default: {
        return [];
      }
    }
  }, [
    config,
    favoriteIds,
    hiddenGames,
    panelGame,
    panelKind,
    settingTarget,
    settingsSections,
    sortLabel,
    t,
  ]);

  const panelTitle = useMemo(() => {
    switch (panelKind) {
      case "game": {
        return t("bigScreen.gameMenuTitle", {
          name: panelGame?.name ?? "",
        });
      }
      case "game-status": {
        return t("bigScreen.statusPickerTitle");
      }
      case "main": {
        return t("bigScreen.mainMenu");
      }
      case "hidden": {
        return t("bigScreen.hiddenGames");
      }
      case "confirm-hide":
      case "confirm-delete":
      case "confirm-reset": {
        return t("bigScreen.confirmTitle");
      }
      case "setting-choices": {
        const setting = settingTarget
          ? settingsSections[settingTarget.sectionIndex]?.settings[
            settingTarget.itemIndex
          ]
          : undefined;
        return setting ? setting.label : "";
      }
      default: {
        return "";
      }
    }
  }, [panelGame?.name, panelKind, settingsSections, settingTarget, t]);

  const panelHint = useMemo(() => {
    const keys = resolveBigScreenKeyGlyphs(keyStyle);
    return t("bigScreen.panelHint", {
      back: keys.back,
      confirm: keys.confirm,
      move: "↑↓",
    });
  }, [keyStyle, t]);

  const safeShelfIndex = Math.min(shelfIndex, Math.max(0, games.length - 1));
  const focusedGame = games[safeShelfIndex];
  const { isFavorite, setFavorite } = useGameFavorite(focusedGame?.id);
  const tags = useGameTags(focusedGame?.id);

  // 详情层的操作条目数：启动 + 详细 恒有，「观看 PV」有预告片时才补一条
  const detailActionCount = focusedGame?.trailer_path ? 3 : 2;

  // 详情层 / 全屏播放器打开时把其余区域清空，让它成为唯一有内容的区域：
  // 否则上下键会顺着 verticalNeighbor 跳出浮层。
  // 面板与设置层有自己的意图处理（打开时先于这里吃掉输入），所以**不动 zones**，
  // 免得把详情层的焦点下标清零。
  const zones = useMemo<FocusZone[]>(
    () =>
      detailsOpen
        ? [
            { id: BIG_SCREEN_RAIL_ZONE, rowLengths: [0] },
            { id: BIG_SCREEN_SHELF_ZONE, rowLengths: [0] },
            { id: BIG_SCREEN_ACTIONS_ZONE, rowLengths: [0] },
            { id: BIG_SCREEN_DETAILS_ZONE, rowLengths: [detailActionCount] },
          ]
        : [
            {
              id: BIG_SCREEN_RAIL_ZONE,
              rowLengths: BIG_SCREEN_CATEGORIES.map(() => 1),
            },
            { id: BIG_SCREEN_SHELF_ZONE, rowLengths: [games.length] },
            { id: BIG_SCREEN_ACTIONS_ZONE, rowLengths: [ACTION_COUNT] },
            { id: BIG_SCREEN_DETAILS_ZONE, rowLengths: [0] },
          ],
    [detailActionCount, detailsOpen, games.length],
  );

  // 背景预告片：偏好关掉 / 低档 / 仅详情层 / 浮层打开时都不起播
  const backgroundTrailerActive = useTrailerHover(
    focusedGame?.id,
    focusedGame?.trailer_path,
    trailerEnabled
    && effectLevel !== "off"
    && !trailerDetailsOnly
    && !overlayOpen
    && !detailsOpen
    && !trailerOpen,
    trailerDelayMs,
  );

  const { focus, move, position } = useFocusEngine({
    onBoundary: (zoneId, direction) =>
      boundaryHandlerRef.current(zoneId, direction),
    verticalOrder: BIG_SCREEN_FOCUS_ORDER,
    zones,
  });

  // ===== 数据变更后的刷新 =====
  const refreshAfterMutation = useCallback(() => {
    invalidateAllGameLists();
    setReloadToken(token => token + 1);
  }, []);

  // 面板换种类时把焦点拉回第一个可选项（避免沿用上一份列表的下标）
  useEffect(() => {
    if (!panelKind) {
      return;
    }
    // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
    setPanelIndex((current) => {
      const first = firstFocusableIndex(panelItems);
      if (
        current >= 0
        && panelItems[current]
        && !panelItems[current].separator
      ) {
        return current;
      }
      return first < 0 ? 0 : first;
    });
  }, [panelItems, panelKind]);

  const selectCategory = useCallback(
    (next: BigScreenCategoryId) => {
      if (next === activeCategory) {
        return;
      }
      // 离开当前分类前，把它选中的卡片位置记下来（切回来时复原）
      shelfMemoryRef.current.set(activeCategory, shelfIndexRef.current);
      setActiveCategory(next);
      setShelfIndex(0);
    },
    [activeCategory],
  );

  const stepCategory = useCallback(
    (delta: number) => {
      const nextIndex
        = (activeCategoryIndex + delta + CATEGORY_COUNT) % CATEGORY_COUNT;
      selectCategory(BIG_SCREEN_CATEGORIES[nextIndex].id);
    },
    [activeCategoryIndex, selectCategory],
  );

  const handleBoundary = useCallback(
    (zoneId: string, direction: FocusDirection) => {
      if (zoneId === BIG_SCREEN_RAIL_ZONE) {
        // 侧栏里 ↑↓ 由引擎在分类之间移动焦点（每行 1 项），这里只管边界：
        // → 回货架、← 吞掉。**不再用 ←/→ 切分类** —— 那会只改 activeCategory
        // 而焦点下标不动，两者脱节（手机端侧栏也是 ← 不处理、→ 回内容区）。
        if (direction === "right" && games.length > 0) {
          focus(BIG_SCREEN_SHELF_ZONE, safeShelfIndex);
        }
        return true;
      }

      if (zoneId === BIG_SCREEN_SHELF_ZONE && direction === "left") {
        // 卡片排最左边再往左 = 进分类栏（对齐手机端 DIR_LEFT 且 col==0）
        focus(BIG_SCREEN_RAIL_ZONE, activeCategoryIndex);
        return true;
      }

      return false;
    },
    [activeCategoryIndex, focus, games.length, safeShelfIndex],
  );

  useEffect(() => {
    boundaryHandlerRef.current = handleBoundary;
  }, [handleBoundary]);

  // 详情层开关时显式交接焦点：引擎的 setZones 只会把失效焦点退回区域首项，
  // 打开时进不了详情层按钮、关闭时会停在侧栏，所以这里各补一次。
  // （播放器是全屏浮层，打开时吞掉全部输入，不需要焦点区。）
  const detailsOpenRef = useRef(detailsOpen);
  useEffect(() => {
    if (detailsOpenRef.current === detailsOpen) {
      return;
    }
    detailsOpenRef.current = detailsOpen;
    focus(
      detailsOpen ? BIG_SCREEN_DETAILS_ZONE : BIG_SCREEN_SHELF_ZONE,
      detailsOpen ? 0 : safeShelfIndex,
    );
  }, [detailsOpen, focus, safeShelfIndex]);

  // 新分类的数据到位后，恢复该分类上次选中的卡片（没有记忆就回第一张）
  useEffect(() => {
    if (games.length === 0 || restoredCategoryRef.current === activeCategory) {
      return;
    }
    restoredCategoryRef.current = activeCategory;
    const saved = shelfMemoryRef.current.get(activeCategory) ?? 0;
    const next = Math.min(Math.max(saved, 0), Math.max(0, games.length - 1));
    // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
    setShelfIndex(next);
    focus(BIG_SCREEN_SHELF_ZONE, next);
  }, [activeCategory, focus, games.length]);

  // 焦点进入侧栏后仍要保留「当前选中的游戏」，所以单独记一份货架下标。
  useEffect(() => {
    if (position.zoneId === BIG_SCREEN_SHELF_ZONE) {
      // eslint-disable-next-line react-hooks-extra/no-direct-set-state-in-use-effect
      setShelfIndex(position.index);
    }
  }, [position]);

  // 给「切分类时保存记忆」提供实时下标（不能在 selectCategory 里直接依赖 shelfIndex，
  // 否则每次焦点移动都会重建那个回调）。
  useEffect(() => {
    shelfIndexRef.current = shelfIndex;
  }, [shelfIndex]);

  // ===== 动作 =====
  const handleStartGame = useCallback(
    (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      void startGame(game);
    },
    [startGame],
  );

  /** 打开详情层（手机端的 Ⓨ / 长按卡片） */
  const handleOpenDetails = useCallback(
    (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      setDetailsOpen(true);
    },
    [],
  );

  /** 打开游戏操作菜单（手机端 S4）：详情层的「详细」与信息层的「更多」都走这里 */
  const handleOpenGameMenu = useCallback(
    (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      // 详情层只在浮层关闭时渲染（`detailsOpen && !overlayOpen`），所以菜单关闭后
      // 它会**重新出现** —— 用户点了「游玩状态」却发现自己又被丢回详情层，看起来
      // 像「跳进了游戏详情」。菜单是从货架出发的一次操作入口，干脆把详情层收起来，
      // 关掉菜单就回到货架（「编辑信息」仍然按原样跳出大屏）。
      setDetailsOpen(false);
      openPanel("game", game);
    },
    [openPanel],
  );

  /** 跳出大屏去看完整详情页（手机端的「编辑信息」= 回到触摸模式） */
  const handleEditGame = useCallback(
    (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      void navigate({ to: `/game/${game.id}` });
    },
    [navigate],
  );

  const handleToggleFavorite = useCallback(
    async (game?: models.Game) => {
      const target = game ?? focusedGame;
      const gameId = target?.id;
      if (!gameId) {
        return;
      }

      try {
        const favoritesId = await resolveFavoritesCategoryId();
        if (!favoritesId) {
          toast.error(t("bigScreen.favoriteFailed"));
          return;
        }

        const currentlyFavorite = favoriteIds.has(gameId);
        if (currentlyFavorite) {
          await RemoveGameFromCategory(gameId, favoritesId);
          setFavoriteIds((current) => {
            const next = new Set(current);
            next.delete(gameId);
            return next;
          });
          if (gameId === focusedGame?.id) {
            setFavorite(false);
          }
          toast.success(t("bigScreen.favoriteRemoved"));
          showBanner(t("bigScreen.favoriteRemoved"), "i-mdi-heart-outline");
          // 在「收藏」分类里取消收藏需要即时刷新列表
          if (activeCategory === "favorites") {
            refreshAfterMutation();
          }
          return;
        }

        await AddGameToCategory(gameId, favoritesId);
        setFavoriteIds(current => new Set(current).add(gameId));
        if (gameId === focusedGame?.id) {
          setFavorite(true);
        }
        toast.success(t("bigScreen.favoriteAdded"));
        showBanner(t("bigScreen.favoriteAdded"), "i-mdi-heart");
      }
      catch (error) {
        console.error("Failed to toggle big screen favorite:", error);
        toast.error(t("bigScreen.favoriteFailed"));
      }
    },
    [
      activeCategory,
      favoriteIds,
      focusedGame,
      refreshAfterMutation,
      setFavorite,
      showBanner,
      t,
    ],
  );

  /**
   * 直接把游玩状态设成指定档（不再「点一次换一档」）。
   *
   * 面板已关闭后再调用，所以只负责写库 + 刷新 + 反馈；选了当前状态就直接返回，
   * 不发无谓的请求，也不弹「已更新」打扰用户。
   */
  const handleSetStatus = useCallback(
    async (game: models.Game | null | undefined, status: enums.GameStatus) => {
      if (!game?.id) {
        return;
      }
      if (game.status === status) {
        return;
      }
      try {
        await BatchUpdateStatus([game.id], status);
        refreshAfterMutation();
        const labelKey
          = STATUS_CHOICES.find(option => option.value === status)?.label ?? "";
        const text = t("bigScreen.menuStatus", { status: t(labelKey) });
        toast.success(t("bigScreen.statusChanged"));
        showBanner(text, "i-mdi-check-circle-outline");
      }
      catch (error) {
        console.error("Failed to set big screen status:", error);
        toast.error(t("bigScreen.statusChangeFailed"));
      }
    },
    [refreshAfterMutation, showBanner, t],
  );

  const handleSetTrailer = useCallback(
    async (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      try {
        const result = await SelectGameTrailer(game.id, game.trailer_path);
        if (result) {
          refreshAfterMutation();
          toast.success(t("bigScreen.trailerBound"));
        }
      }
      catch (error) {
        console.error("Failed to bind trailer:", error);
      }
    },
    [refreshAfterMutation, t],
  );

  const handleRemoveTrailer = useCallback(
    async (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      try {
        await RemoveGameTrailer(game.id);
        refreshAfterMutation();
        toast.success(t("bigScreen.trailerRemoved"));
      }
      catch (error) {
        console.error("Failed to remove trailer:", error);
      }
    },
    [refreshAfterMutation, t],
  );

  // 大屏自定义标题图/背景图（对齐手机端 M10 requestArtPick / clearArt）
  const handleSetArt = useCallback(
    async (game: models.Game | null | undefined, kind: string) => {
      if (!game?.id) {
        return;
      }
      try {
        const current
          = kind === "bg" ? (game.bg_path ?? "") : (game.logo_path ?? "");
        const result = await SelectGameArt(game.id, kind, current);
        if (result) {
          refreshAfterMutation();
          toast.success(
            t(
              kind === "bg" ? "bigScreen.bgArtBound" : "bigScreen.logoArtBound",
            ),
          );
        }
      }
      catch (error) {
        console.error("Failed to set big screen art:", error);
        toast.error(t("bigScreen.artImportFailed"));
      }
    },
    [refreshAfterMutation, t],
  );

  const handleClearArt = useCallback(
    async (game: models.Game | null | undefined, kind: string) => {
      if (!game?.id) {
        return;
      }
      try {
        await ClearGameArt(game.id, kind);
        refreshAfterMutation();
        toast.success(t("bigScreen.artCleared"));
      }
      catch (error) {
        console.error("Failed to clear big screen art:", error);
        toast.error(t("bigScreen.artImportFailed"));
      }
    },
    [refreshAfterMutation, t],
  );

  const handleOpenDirectory = useCallback(
    async (game: models.Game | null | undefined) => {
      if (!game) {
        return;
      }
      const directory = resolveGameDirectory(game);
      if (!directory) {
        toast.error(t("bigScreen.openDirectoryFailed"));
        return;
      }
      try {
        await OpenLocalPath(directory);
      }
      catch (error) {
        console.error("Failed to open game directory:", error);
        toast.error(t("bigScreen.openDirectoryFailed"));
      }
    },
    [t],
  );

  const handleSetHidden = useCallback(
    async (game: models.Game | null | undefined, hidden: boolean) => {
      if (!game?.id) {
        return;
      }
      try {
        await SetGameHidden(game.id, hidden);
        refreshAfterMutation();
        toast.success(
          hidden ? t("bigScreen.gameHidden") : t("bigScreen.gameUnhidden"),
        );
      }
      catch (error) {
        console.error("Failed to change big screen hidden state:", error);
        toast.error(t("bigScreen.hiddenChangeFailed"));
      }
    },
    [refreshAfterMutation, t],
  );

  const handleDeleteGame = useCallback(
    async (game: models.Game | null | undefined) => {
      if (!game?.id) {
        return;
      }
      try {
        await DeleteGame(game.id);
        refreshAfterMutation();
        toast.success(t("bigScreen.gameDeleted"));
      }
      catch (error) {
        console.error("Failed to delete game from big screen:", error);
        toast.error(t("bigScreen.deleteFailed"));
      }
    },
    [refreshAfterMutation, t],
  );

  const handleRandomGame = useCallback(() => {
    if (games.length === 0) {
      return;
    }
    const index = Math.floor(Math.random() * games.length);
    setShelfIndex(index);
    focus(BIG_SCREEN_SHELF_ZONE, index);
  }, [focus, games.length]);

  const handleSwitchSort = useCallback(() => {
    const nextIndex
      = (BIG_SCREEN_SORT_MODES.indexOf(sortMode) + 1)
        % BIG_SCREEN_SORT_MODES.length;
    const next = BIG_SCREEN_SORT_MODES[nextIndex];
    setSortMode(next);
    setShelfIndex(0);
    const label
      = next === "name"
        ? t("bigScreen.sortName")
        : next === "newest"
          ? t("bigScreen.sortNewest")
          : t("bigScreen.sortRecent");
    showBanner(t("bigScreen.sortChanged", { sort: label }), "i-mdi-sort");
  }, [showBanner, sortMode, t]);

  const exitBigScreen = useCallback(() => {
    // 「记住筛选」打开时把当前分类存下来，下次进大屏直接回到这里
    if (rememberFilter) {
      void patchLiveConfig({ bigscreen_last_category: activeCategory });
    }
    void navigate({ to: BIG_SCREEN_EXIT_PATH });
  }, [activeCategory, navigate, patchLiveConfig, rememberFilter]);

  const playSound = useCallback(
    (kind: BigScreenSound) => {
      if (!soundEnabled) {
        return;
      }
      // 焦点移动音可以单独关掉（手机端的「焦点音」开关）
      if (kind === "focus" && !focusTicks) {
        return;
      }
      playBigScreenSound(kind, soundVolume / 100);
    },
    [focusTicks, soundEnabled, soundVolume],
  );

  const markInputDevice = useCallback((device: BigScreenInputDevice) => {
    setInputDevice(current => (current === device ? current : device));
  }, []);

  // ===== 面板条目执行 =====
  const activatePanelItem = useCallback(
    (index: number) => {
      const item = focusedPanelItem(panelItems, index);
      if (!item) {
        return;
      }
      const key = item.key;

      if (key === "cancel" || key === "back") {
        if (key === "back") {
          openPanel("main");
          return;
        }
        // 二次确认面板里的「取消」退回**游戏操作菜单**（对齐手机端的「取消，不隐藏」），
        // 而不是把整个面板关掉：用户多半只是想换个操作，不该被丢回货架重新走一遍。
        if (
          (panelKind === "confirm-hide" || panelKind === "confirm-delete")
          && panelGame
        ) {
          openPanel("game", panelGame);
          return;
        }
        closePanel();
        return;
      }
      if (key === "confirm") {
        const kind = panelKind;
        closePanel();
        if (kind === "confirm-hide") {
          void handleSetHidden(panelGame, true);
          return;
        }
        if (kind === "confirm-delete") {
          void handleDeleteGame(panelGame);
          return;
        }
        if (kind === "confirm-reset") {
          // 恢复大屏默认设置：只重置 bigscreen_* 自身，主库设置不受影响
          void patchLiveConfig(BIG_SCREEN_DEFAULT_CONFIG);
          setSortMode("recent");
          setActiveCategory(
            normalizeCategoryId(
              BIG_SCREEN_DEFAULT_CONFIG.bigscreen_default_category,
            ),
          );
          shelfMemoryRef.current.clear();
          showBanner(t("bigScreen.defaultsRestored"));
        }
        return;
      }
      if (key.startsWith("hidden:")) {
        const gameId = key.slice("hidden:".length);
        const game = hiddenGames.find(entry => entry.id === gameId);
        void handleSetHidden(game, false);
        closePanel();
        return;
      }
      if (key.startsWith("status:")) {
        // 选中某一档游玩状态：写库 → 刷新（状态会进分类筛选与卡片角标）
        const status = key.slice("status:".length) as enums.GameStatus;
        const game = panelGame;
        closePanel();
        void handleSetStatus(game ?? undefined, status);
        return;
      }
      if (key.startsWith("choice:")) {
        const value = key.slice("choice:".length);
        const setting = settingTarget
          ? settingsSections[settingTarget.sectionIndex]?.settings[
            settingTarget.itemIndex
          ]
          : undefined;
        closePanel();
        if (setting && config && setting.write) {
          void patchLiveConfig(
            setting.write(config, value) as Partial<NonNullable<typeof config>>,
          );
        }
        return;
      }

      switch (key) {
        case "start": {
          const game = panelGame;
          closePanel();
          handleStartGame(game ?? undefined);
          return;
        }
        case "favorite": {
          void handleToggleFavorite(panelGame ?? undefined);
          closePanel();
          return;
        }
        case "status": {
          // 打开状态选择面板（不是就地循环），选完才落库
          const game = panelGame;
          openPanel("game-status", game ?? undefined);
          return;
        }
        case "trailer-set": {
          const game = panelGame;
          closePanel();
          void handleSetTrailer(game ?? undefined);
          return;
        }
        case "logo-set": {
          const game = panelGame;
          closePanel();
          void handleSetArt(game ?? undefined, "logo");
          return;
        }
        case "logo-remove": {
          const game = panelGame;
          closePanel();
          void handleClearArt(game ?? undefined, "logo");
          return;
        }
        case "bg-set": {
          const game = panelGame;
          closePanel();
          void handleSetArt(game ?? undefined, "bg");
          return;
        }
        case "bg-remove": {
          const game = panelGame;
          closePanel();
          void handleClearArt(game ?? undefined, "bg");
          return;
        }
        case "trailer-remove": {
          const game = panelGame;
          closePanel();
          void handleRemoveTrailer(game ?? undefined);
          return;
        }
        case "edit": {
          const game = panelGame;
          closePanel();
          handleEditGame(game ?? undefined);
          return;
        }
        case "open-dir": {
          const game = panelGame;
          closePanel();
          void handleOpenDirectory(game ?? undefined);
          return;
        }
        case "hide": {
          const game = panelGame;
          if (game?.hidden) {
            closePanel();
            void handleSetHidden(game, false);
            return;
          }
          openPanel("confirm-hide", game ?? undefined);
          return;
        }
        case "delete": {
          const game = panelGame;
          openPanel("confirm-delete", game ?? undefined);
          return;
        }
        case "settings": {
          closePanel();
          setSettingsOpen(true);
          return;
        }
        case "hidden": {
          setHiddenGames([]);
          openPanel("hidden");
          return;
        }
        case "random": {
          closePanel();
          handleRandomGame();
          return;
        }
        case "sort": {
          handleSwitchSort();
          return;
        }
        case "shortcuts": {
          closePanel();
          toast.success(t("bigScreen.shortcutsHint"));
          return;
        }
        case "exit": {
          closePanel();
          exitBigScreen();
          return;
        }
        default: {
          closePanel();
        }
      }
    },
    [
      closePanel,
      config,
      exitBigScreen,
      handleClearArt,
      handleDeleteGame,
      handleEditGame,
      handleOpenDirectory,
      handleRandomGame,
      handleRemoveTrailer,
      handleSetArt,
      handleSetHidden,
      handleSetStatus,
      handleSetTrailer,
      handleStartGame,
      handleSwitchSort,
      handleToggleFavorite,
      hiddenGames,
      openPanel,
      panelGame,
      panelItems,
      panelKind,
      patchLiveConfig,
      settingTarget,
      settingsSections,
      showBanner,
      t,
    ],
  );

  const handlePanelIntent = useCallback(
    (intent: BigScreenIntent) => {
      switch (intent.type) {
        case "move": {
          if (intent.direction === "up" || intent.direction === "down") {
            setPanelIndex(current =>
              stepFocusableIndex(
                panelItems,
                current,
                intent.direction === "down" ? 1 : -1,
              ),
            );
          }
          return;
        }
        case "confirm": {
          activatePanelItem(panelIndex);
          return;
        }
        case "back": {
          if (panelKind === "main") {
            closePanel();
            return;
          }
          if (panelKind === "hidden") {
            openPanel("main");
            return;
          }
          if (
            panelKind === "confirm-hide"
            || panelKind === "confirm-delete"
            || panelKind === "confirm-reset"
            || panelKind === "setting-choices"
          ) {
            closePanel();
            return;
          }
          closePanel();
          return;
        }
        case "favorite": {
          // 面板里按收藏键 = 直接切换当前游戏收藏，省一次进出
          if (panelGame) {
            void handleToggleFavorite(panelGame);
          }
        }
        // 其余意图（切分类 / Ⓨ 再叠一层）一律吞掉，避免误操作到下层
      }
    },
    [
      activatePanelItem,
      closePanel,
      handleToggleFavorite,
      openPanel,
      panelGame,
      panelIndex,
      panelItems,
      panelKind,
    ],
  );

  // ===== 详情层 =====
  const detailActions = useMemo<BigScreenDetailAction[]>(() => {
    const items: BigScreenDetailAction[] = [
      {
        icon: "i-mdi-play",
        key: "start",
        label: t("gameCard.startGame"),
        run: () => handleStartGame(focusedGame),
      },
    ];
    // 「观看 PV」有才出现（手机端 M15：没 PV 就整条隐藏）
    if (focusedGame?.trailer_path) {
      items.push({
        icon: "i-mdi-movie-open-outline",
        key: "trailer",
        label: t("bigScreen.trailer"),
        run: () => setTrailerOpen(true),
      });
    }
    items.push({
      icon: "i-mdi-dots-horizontal",
      key: "details",
      label: t("bigScreen.more"),
      run: () => handleOpenGameMenu(focusedGame),
    });
    return items;
  }, [focusedGame, handleOpenGameMenu, handleStartGame, t]);

  const scrollDetailsSummary = useCallback((direction: number) => {
    const element = document.querySelector<HTMLElement>(
      "[data-bigscreen-details-scroll]",
    );
    if (!element) {
      return;
    }
    element.scrollBy({
      behavior: "smooth",
      top: direction * Math.max(60, element.clientHeight / 2),
    });
  }, []);

  const handleDetailsIntent = useCallback(
    (intent: BigScreenIntent) => {
      switch (intent.type) {
        case "move": {
          if (intent.direction === "up" || intent.direction === "down") {
            scrollDetailsSummary(intent.direction === "down" ? 1 : -1);
            return;
          }
          move(intent.direction);
          return;
        }
        case "confirm": {
          const action = detailActions[position.index];
          if (action?.disabled) {
            return;
          }
          action?.run();
          return;
        }
        case "back": {
          setDetailsOpen(false);
          return;
        }
        case "favorite": {
          void handleToggleFavorite(focusedGame);
          return;
        }
        case "details": {
          handleOpenGameMenu(focusedGame);
        }
        // 详情层里切分类不做任何事
      }
    },
    [
      detailActions,
      focusedGame,
      handleOpenGameMenu,
      handleToggleFavorite,
      move,
      position.index,
      scrollDetailsSummary,
    ],
  );

  // ===== 主界面 =====
  const actions = useMemo<BigScreenAction[]>(
    () => [
      {
        icon: "i-mdi-play",
        key: "start",
        label: t("gameCard.startGame"),
        run: () => handleStartGame(focusedGame),
      },
      {
        icon: "i-mdi-heart-outline",
        key: "favorite",
        label: isFavorite ? t("bigScreen.unfavorite") : t("bigScreen.favorite"),
        run: () => {
          void handleToggleFavorite(focusedGame);
        },
      },
      {
        icon: "i-mdi-information-variant",
        key: "details",
        label: t("common.details"),
        run: () => handleOpenDetails(focusedGame),
      },
      {
        icon: "i-mdi-dots-horizontal",
        key: "more",
        label: t("bigScreen.more"),
        run: () => handleOpenGameMenu(focusedGame),
      },
    ],
    [
      focusedGame,
      handleOpenDetails,
      handleOpenGameMenu,
      handleStartGame,
      handleToggleFavorite,
      isFavorite,
      t,
    ],
  );

  const handleMainIntent = useCallback(
    (intent: BigScreenIntent) => {
      switch (intent.type) {
        case "move": {
          move(intent.direction);
          return;
        }
        case "confirm": {
          if (position.zoneId === BIG_SCREEN_RAIL_ZONE) {
            // 侧栏里 Ⓐ = 应用当前聚焦的分类，然后回货架（对齐手机端 setRailZone(false)）。
            // 之前这里只是回货架，分类根本没切 —— 键盘/手柄用户选不了分类。
            const next = BIG_SCREEN_CATEGORIES[position.index];
            if (next) {
              selectCategory(next.id);
            }
            if (games.length > 0) {
              focus(BIG_SCREEN_SHELF_ZONE, 0);
            }
            return;
          }
          if (position.zoneId === BIG_SCREEN_ACTIONS_ZONE) {
            actions[position.index]?.run();
            return;
          }
          handleStartGame(focusedGame);
          return;
        }
        case "back": {
          exitBigScreen();
          return;
        }
        case "category": {
          stepCategory(intent.delta);
          return;
        }
        case "favorite": {
          void handleToggleFavorite(focusedGame);
          return;
        }
        case "details": {
          handleOpenDetails(focusedGame);
          return;
        }
        case "menu": {
          openPanel("main");
        }
      }
    },
    [
      actions,
      exitBigScreen,
      focus,
      focusedGame,
      games.length,
      handleOpenDetails,
      handleStartGame,
      handleToggleFavorite,
      move,
      openPanel,
      position.index,
      position.zoneId,
      selectCategory,
      stepCategory,
    ],
  );

  /** 键盘与手柄共用的意图分发：两者只在「按键怎么翻译」上不同 */
  const dispatchIntent = useCallback(
    (intent: BigScreenIntent) => {
      // 入场动画：任意输入 = 跳过（对齐手机端 consumeIfIntroPlaying）
      if (introPlaying) {
        // 交给入场层自己走完 240ms 淡出，而不是直接卸载（硬切会闪一下）
        introRef.current?.skip();
        playSound("confirm");
        return;
      }

      if (intent.type !== "move") {
        setHintKey(key => key + 1);
      }

      if (panelOpen) {
        playSound(
          intent.type === "back"
            ? "open"
            : intent.type === "move"
              ? "focus"
              : "confirm",
        );
        handlePanelIntent(intent);
        return;
      }

      if (settingsOpen) {
        playSound(intent.type === "move" ? "focus" : "confirm");
        const consumed = settingsRef.current?.handleIntent(intent) ?? false;
        if (!consumed && intent.type === "move") {
          move(intent.direction);
        }
        return;
      }

      if (trailerOpen) {
        if (intent.type === "back") {
          playSound("open");
          setTrailerOpen(false);
        }
        return;
      }

      if (detailsOpen) {
        playSound(
          intent.type === "back"
            ? "open"
            : intent.type === "move"
              ? "focus"
              : "confirm",
        );
        handleDetailsIntent(intent);
        return;
      }

      switch (intent.type) {
        case "back": {
          playSound("open");
          handleMainIntent(intent);
          return;
        }
        case "category": {
          playSound("focus");
          handleMainIntent(intent);
          return;
        }
        case "confirm": {
          playSound("confirm");
          handleMainIntent(intent);
          return;
        }
        case "details": {
          // 手机端 DETAILS 归到 OPEN 音（打开详情层/菜单都是"打开"这一类）
          playSound("open");
          handleMainIntent(intent);
          return;
        }
        case "favorite": {
          playSound("confirm");
          handleMainIntent(intent);
          return;
        }
        case "menu": {
          playSound("open");
          handleMainIntent(intent);
          return;
        }
        default: {
          playSound("focus");
          handleMainIntent(intent);
        }
      }
    },
    [
      detailsOpen,
      handleDetailsIntent,
      handleMainIntent,
      handlePanelIntent,
      introPlaying,
      move,
      panelOpen,
      playSound,
      settingsOpen,
      trailerOpen,
    ],
  );

  const handleGamepadIntent = useCallback(
    (intent: BigScreenIntent) => {
      markInputDevice("gamepad");
      dispatchIntent(intent);
    },
    [dispatchIntent, markInputDevice],
  );

  const { connected: gamepadConnected } = useGamepad({
    onIntent: handleGamepadIntent,
  });

  /**
   * 手柄连接状态变化时提示一次（对齐手机端 M18-2）。
   *
   * 手机端是在**入场动画结束后**才允许提示 —— 手柄多半在启动前就插着了，
   * 只看插拔事件会什么都看不到；这里同样等 introPlaying 结束再判一次。
   */
  const gamepadBannerRef = useRef<boolean | null>(null);
  useEffect(() => {
    if (introPlaying || gamepadBannerRef.current === gamepadConnected) {
      return;
    }
    gamepadBannerRef.current = gamepadConnected;
    showBanner(
      t(
        gamepadConnected
          ? "bigScreen.gamepadConnected"
          : "bigScreen.gamepadDisconnected",
      ),
      gamepadConnected ? "i-mdi-gamepad-variant" : undefined,
    );
  }, [gamepadConnected, introPlaying, showBanner, t]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const direction = KEY_DIRECTIONS[event.key];
      if (direction) {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ direction, type: "move" });
        return;
      }

      if (event.key === "Escape") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ type: "back" });
        return;
      }

      if (event.key === "Tab") {
        event.preventDefault();
        markInputDevice("keyboard");
        // Tab = 手柄 Start = ☰，统一走 MENU 意图（浮层里会被各自吞掉）
        dispatchIntent({ type: "menu" });
        return;
      }

      if (event.key.toLowerCase() === "q") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ delta: -1, type: "category" });
        return;
      }

      if (event.key.toLowerCase() === "e") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ delta: 1, type: "category" });
        return;
      }

      if (event.key.toLowerCase() === "x") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ type: "favorite" });
        return;
      }

      if (event.key.toLowerCase() === "y") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ type: "details" });
        return;
      }

      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        markInputDevice("keyboard");
        dispatchIntent({ type: "confirm" });
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [dispatchIntent, markInputDevice, openPanel]);

  const railFocused = position.zoneId === BIG_SCREEN_RAIL_ZONE;
  const isShelfFocused = position.zoneId === BIG_SCREEN_SHELF_ZONE;
  const coverUrl
    = focusedGame?.cover_url || focusedGame?.cover_source_url || "";
  // 对齐手机端 bgUriOf：自定义背景图优先，否则退回封面；
  // 用了背景图时不再叠高清封面层（自定义图本身就是原始分辨率）
  const backgroundUrl = focusedGame?.bg_path || coverUrl;
  const backgroundSourceUrl = focusedGame?.bg_path
    ? ""
    : focusedGame?.cover_source_url || "";
  // 用户还没用过任何输入设备时，按手柄是否接入决定提示条形态
  const hintDevice: BigScreenInputDevice
    = inputDevice ?? (gamepadConnected ? "gamepad" : "keyboard");

  const contentHidden = trailerOpen;

  return (
    // `dark` 是刻意加的：大屏整体是暗色皮肤（对齐手机端 bs_* 常量），
    // 而复用的组件是主题相关的。不加这一层，亮色主题下会很突兀。
    <div className="dark relative flex h-screen w-screen select-none overflow-hidden bg-brand-900 text-white">
      <BigScreenBackground
        coverUrl={backgroundUrl}
        coverSourceUrl={backgroundSourceUrl}
        isNSFW={Boolean(focusedGame?.is_nsfw)}
        trailerFit={pvFit}
        trailerMuted={trailerMuted}
        trailerScrimOpacity={
          pvScrim && backgroundTrailerActive ? (pvScrimPercent / 100) * 0.62 : 0
        }
        trailerUrl={focusedGame?.trailer_path}
        backgroundTrailerActive={backgroundTrailerActive && trailerEnabled}
      />
      <BigScreenAtmosphere
        coverUrl={coverUrl}
        level={snowEnabled ? effectLevel : "off"}
        seed={focusedGame?.id ?? ""}
      />

      <div
        className={`relative z-10 flex h-full w-full flex-col transition-opacity duration-200 ${
          contentHidden ? "pointer-events-none opacity-0" : "opacity-100"
        } ${contentRevealing ? "animate-bigscreen-reveal" : ""}`}
      >
        <BigScreenTopBar
          gamepadConnected={gamepadConnected}
          onOpenMenu={() => openPanel("main")}
        />

        <div className="flex min-h-0 flex-1">
          {/*
            侧栏固定占 76px（对齐手机端 bsRail 的 72dp），展开时由 nav 自己
            绝对定位浮出去 —— 所以这里宽度恒定，货架永远不会被推着左右跳。
          */}
          <div
            className="relative h-full w-[76px] shrink-0"
            onMouseEnter={() => setRailHovered(true)}
            onMouseLeave={() => {
              setRailHovered(false);
              // 悬停会把焦点带进侧栏（rail 条目的 onMouseEnter），鼠标离开后必须
              // 交还出去：侧栏的展开态是「悬停 或 焦点在侧栏」，只清悬停标记不够。
              if (position.zoneId !== BIG_SCREEN_RAIL_ZONE) {
                return;
              }
              if (games.length > 0) {
                focus(BIG_SCREEN_SHELF_ZONE, safeShelfIndex);
                return;
              }
              focus(BIG_SCREEN_RAIL_ZONE, activeCategoryIndex);
            }}
          >
            <BigScreenRail
              activeCategory={activeCategory}
              counts={categoryCounts}
              entryAnimation={entryAnimation}
              expanded={railFocused || railHovered || railPinned}
              focused={railFocused}
              focusedIndex={position.index}
              onFocusIndexChange={index => focus(BIG_SCREEN_RAIL_ZONE, index)}
              onSelect={id => selectCategory(id)}
            />
          </div>

          {/*
            测量容器只负责给出「可用空间」，内容绝对定位在里面：
            对齐手机端 `bsShelfContainer` 的 `gravity="bottom"` ——
            行标题紧贴卡片排上方，卡片排贴着信息浮层的上沿。
          */}
          <div ref={shelfAreaRef} className="relative min-h-0 flex-1">
            <div className="absolute inset-0 flex flex-col justify-end px-10 pb-2">
              <div
                className="flex shrink-0 flex-col justify-end"
                style={{ minHeight: shelfMetrics.infoReserveHeight }}
              >
                <BigScreenInfoBar
                  actions={actions}
                  actionsFocused={position.zoneId === BIG_SCREEN_ACTIONS_ZONE}
                  focusedActionIndex={position.index}
                  game={focusedGame}
                  isFavorite={isFavorite}
                  onActionActivate={(index) => {
                    focus(BIG_SCREEN_ACTIONS_ZONE, index);
                    actions[index]?.run();
                  }}
                  onActionFocus={index =>
                    focus(BIG_SCREEN_ACTIONS_ZONE, index)}
                  tags={tags}
                />
              </div>

              {activeCategoryLabelKey && (
                <h2
                  className="flex shrink-0 items-end gap-3 pb-1 text-xl font-bold text-white drop-shadow-[0_2px_10px_rgba(0,0,0,0.5)]"
                  style={{ height: shelfMetrics.headerHeight }}
                >
                  {t(activeCategoryLabelKey)}
                  {games.length > 0 && (
                    <span className="text-xs font-normal text-brand-400">
                      {t("category.gameCount", { count: games.length })}
                    </span>
                  )}
                </h2>
              )}

              {games.length > 0 && shelfMetrics.rowHeight > 0 && (
                // -mx-2 抵消货架内部给焦点缩放留的 px-2，保持左沿与标题对齐
                <div className="-mx-2">
                  <VirtualGameShelf
                    cardWidth={shelfMetrics.cardWidth}
                    coverHeight={shelfMetrics.coverHeight}
                    entryAnimation={entryAnimation}
                    favoriteIds={favoriteIds}
                    focused={isShelfFocused}
                    focusedIndex={safeShelfIndex}
                    focusScale={focusScale}
                    games={games}
                    onActivate={handleStartGame}
                    onDetails={handleOpenDetails}
                    onFocusIndexChange={index =>
                      focus(BIG_SCREEN_SHELF_ZONE, index)}
                    rowHeight={shelfMetrics.rowHeight}
                    showTitles={showTitles}
                  />
                </div>
              )}

              {games.length === 0 && (
                <div className="flex flex-1 items-center justify-center text-sm text-brand-400">
                  {t("bigScreen.empty")}
                </div>
              )}
            </div>
          </div>
        </div>

        <BigScreenHintBar
          key={hintKey}
          className="shrink-0 px-10 pb-5"
          hintMode={hintMode}
          inputDevice={hintDevice}
          keyStyle={keyStyle}
          variant="main"
        />
      </div>

      {detailsOpen && focusedGame && !overlayOpen && (
        <BigScreenDetailsLayer
          actions={detailActions}
          actionsFocused={position.zoneId === BIG_SCREEN_DETAILS_ZONE}
          focusedActionIndex={position.index}
          game={focusedGame}
          hintMode={hintMode}
          inputDevice={hintDevice}
          keyStyle={keyStyle}
          onActionActivate={(index) => {
            focus(BIG_SCREEN_DETAILS_ZONE, index);
            detailActions[index]?.run();
          }}
          onActionFocus={index => focus(BIG_SCREEN_DETAILS_ZONE, index)}
          onClose={() => setDetailsOpen(false)}
          tags={tags}
        />
      )}

      {panelOpen && (
        <BigScreenPanel
          focusedIndex={panelIndex}
          hint={panelHint}
          items={panelItems}
          onActivate={activatePanelItem}
          onClose={closePanel}
          onFocusIndexChange={setPanelIndex}
          title={panelTitle}
        />
      )}

      {settingsOpen && config && (
        <BigScreenSettingsLayer
          ref={settingsRef}
          config={config}
          onChange={(next) => {
            void patchLiveConfig(next as Partial<NonNullable<typeof config>>);
          }}
          onClose={() => setSettingsOpen(false)}
          onOpenChoices={(sectionIndex, itemIndex) => {
            setSettingTarget({ itemIndex, sectionIndex });
            setPanelKind("setting-choices");
            setPanelIndex(0);
          }}
          sections={settingsSections}
        />
      )}

      {trailerOpen && focusedGame?.trailer_path && (
        <BigScreenTrailerPlayer
          fit={pvFit}
          muted={trailerMuted}
          title={focusedGame.name}
          url={focusedGame.trailer_path}
          onClose={() => setTrailerOpen(false)}
        />
      )}

      {introPlaying && (
        <BigScreenIntro
          ref={introRef}
          onFinished={() => setIntroPlaying(false)}
          onRevealStart={handleIntroReveal}
          richEffects={effectLevel !== "off"}
          videoUrl={introVideo}
        />
      )}

      {/* 顶部提示条：操作反馈 + 手柄连接状态（入场动画期间抑制） */}
      <BigScreenBanner
        holdMs={bannerHoldMs}
        message={banner}
        suppressed={introPlaying}
      />
    </div>
  );
}
