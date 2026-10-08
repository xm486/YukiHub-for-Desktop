import { create } from "zustand";

import type {
  appconf,
  enums,
  launcher,
  models,
  service,
  vo,
} from "../src/bindings/models";

import {
  ApplyGameLibraryPathChange,
  GetAppConfig,
  UpdateAppConfig,
} from "../bindings/yukihub/internal/service/configservice";
import { GetHomePageData } from "../bindings/yukihub/internal/service/homeservice";
import {
  StartGameWithOptions,
  StartGameWithTracking,
} from "../bindings/yukihub/internal/service/startservice";
import {
  GetGOOS,
  SupportsBackgroundProcessMute,
} from "../bindings/yukihub/internal/service/versionservice";
import { normalizeEnabledMetadataSources } from "./utils/metadataSources";

type AISummaryCache = {
  [dimension: string]: string;
};

export type GameRuntimeState = "idle" | "launching" | "playing" | "ending";
export type GameRuntimeTimingMode = "wall-clock" | "active";

export type GameRuntimeInfo = {
  activeSeconds?: number;
  game: models.Game | null;
  gameId: string;
  isFocused?: boolean;
  // reason 来自后端事件的 reason 字段，用来区分会话是怎么来的：
  // "launched"=由 YukiHub 启动游戏，"manual-started"=纯手动计时。
  reason?: string;
  // processUnknown=true：后端没识别出游戏进程，会话仍计时，但结束判定
  // 改由「回到 YukiHub」兜底 —— 界面上要让用户知道，否则会以为计时坏了。
  processUnknown?: boolean;
  sessionId: string;
  startTime: unknown;
  state: GameRuntimeState;
  timingMode?: GameRuntimeTimingMode;
};

export type GameRuntimeMap = Record<string, GameRuntimeInfo>;

export type GameRuntimeChangedEvent = {
  active_seconds?: number;
  game?: models.Game | null;
  game_id?: string;
  is_focused?: boolean;
  session_id?: string;
  start_time?: unknown;
  state?: GameRuntimeState;
  reason?: string;
  timing_mode?: GameRuntimeTimingMode;
  /** 进程识别失败降级出来的会话（靠回到 YukiHub 兜底结束） */
  process_unknown?: boolean;
};

export type FetchHomeDataOptions = {
  showLoading?: boolean;
  syncRuntime?: boolean;
};

function normalizeLibraryTags(tags: string[]) {
  return [...new Set(tags.map(tag => tag.trim()).filter(Boolean))];
}

function areStringArraysEqual(left: string[], right: string[]) {
  return (
    left.length === right.length
    && left.every((value, index) => value === right[index])
  );
}

function withSidebarState(
  config: appconf.AppConfig,
  sidebarOpen: boolean,
): appconf.AppConfig {
  return { ...config, sidebar_open: sidebarOpen };
}

export function isGameRuntimeVisible(runtime?: GameRuntimeInfo | null) {
  return (
    runtime?.state === "launching"
    || runtime?.state === "playing"
    || runtime?.state === "ending"
  );
}

function getRuntimeStartTime(runtime: GameRuntimeInfo) {
  const timestamp = Date.parse(String(runtime.startTime ?? ""));
  return Number.isFinite(timestamp) ? timestamp : 0;
}

function getVisibleGameRuntimes(gameRuntimes: GameRuntimeMap) {
  return Object.values(gameRuntimes).filter(isGameRuntimeVisible);
}

function isSameSessionStateRegression(
  currentRuntime: GameRuntimeInfo | undefined,
  eventSessionId: string | undefined,
  nextState: GameRuntimeState,
) {
  if (
    !currentRuntime?.sessionId
    || !eventSessionId
    || currentRuntime.sessionId !== eventSessionId
  ) {
    return false;
  }

  return (
    (nextState === "launching"
      && (currentRuntime.state === "playing"
        || currentRuntime.state === "ending"))
      || (nextState === "playing" && currentRuntime.state === "ending")
  );
}

/**
 * 维护「已收到权威 idle、游戏确实结束」的 gameId 集合。
 *
 * 后端的进程接力宽限是异步的：退出游戏的瞬间先发 idle（计时岛立刻消失），
 * 几秒后才真正把 end_time 落库。这期间首页快照 `recent_played[].is_playing`
 * 仍是 true —— 用户切回首页会重新拉一次快照，于是刚消失的计时岛被这份过期
 * 数据又点亮了（原版 LunaBox 结算与落库是同步的，没有这个窗口）。
 *
 * 事件是权威且即时的，快照只是有延迟的轮询：记下「已经看到它结束」，在收到
 * 下一次 launching/playing/ending 之前，不让快照复活它。
 */
function withEndedGame(
  endedGameIds: Set<string>,
  gameId: string,
  ended: boolean,
) {
  if (endedGameIds.has(gameId) === ended) {
    return endedGameIds;
  }

  const next = new Set(endedGameIds);
  if (ended) {
    next.add(gameId);
  }
  else {
    next.delete(gameId);
  }
  return next;
}

function pickGameRuntime(
  gameRuntimes: GameRuntimeMap,
  preferredGameId: string,
): GameRuntimeInfo | undefined {
  if (preferredGameId && isGameRuntimeVisible(gameRuntimes[preferredGameId])) {
    return gameRuntimes[preferredGameId];
  }

  const visibleRuntimes = getVisibleGameRuntimes(gameRuntimes);
  if (visibleRuntimes.length === 0) {
    return undefined;
  }

  return [...visibleRuntimes].sort(
    (left, right) => getRuntimeStartTime(right) - getRuntimeStartTime(left),
  )[0];
}

function runtimeSelectionPatch(
  gameRuntimes: GameRuntimeMap,
  preferredGameId: string,
) {
  const gameRuntime = pickGameRuntime(gameRuntimes, preferredGameId);

  return {
    activeGameRuntimeId: gameRuntime?.gameId ?? "",
  };
}

type AppState = {
  isSidebarOpen: boolean;
  toggleSidebar: () => void;
  setSidebarOpen: (open: boolean) => void;
  homeData: vo.HomePageData | null;
  config: appconf.AppConfig | null;
  draftConfig: appconf.AppConfig | null;
  enabledMetadataSources: enums.SourceType[];
  platformGOOS: string;
  backgroundProcessMuteSupported: boolean;
  isLoading: boolean;
  gameRuntimes: GameRuntimeMap;
  /** 已收到权威 idle 的 gameId（见 withEndedGame）：阻止滞后的首页快照复活计时岛。 */
  endedGameIds: Set<string>;
  activeGameRuntimeId: string;
  fetchHomeData: (options?: FetchHomeDataOptions) => Promise<void>;
  fetchConfig: () => Promise<void>;
  fetchPlatformGOOS: () => Promise<void>;
  applyGameRuntimeEvent: (event: GameRuntimeChangedEvent) => void;
  setGameRuntimeFromHome: (recentPlayed: vo.LastPlayedGame[] | null) => void;
  selectNextGameRuntime: () => void;
  startGame: (
    game: models.Game,
    options?: Partial<launcher.LaunchOptions>,
  ) => Promise<boolean>;
  patchLiveConfig: (patch: Partial<appconf.AppConfig>) => Promise<void>;
  applyGameLibraryPathChange: (
    newPath: string,
    syncPaths: boolean,
  ) => Promise<service.GameLibraryPathChangeResult>;
  applyCloudSyncStatus: (status: vo.CloudSyncStatus) => void;
  setDraftConfig: (config: appconf.AppConfig) => void;
  resetDraftConfig: () => void;
  saveDraftConfig: () => Promise<void>;
  librarySelectedTags: string[];
  setLibrarySelectedTags: (tags: string[]) => void;
  // AI Summary 缓存
  aiSummaryCache: AISummaryCache;
  setAISummary: (dimension: string, summary: string) => void;
  getAISummary: (dimension: string) => string | undefined;
};

export const useAppStore = create<AppState>((set, get) => ({
  isSidebarOpen: true,
  toggleSidebar: () => {
    const newState = !get().isSidebarOpen;
    set({ isSidebarOpen: newState });
    const config = get().config;
    if (!config) {
      return;
    }

    void UpdateAppConfig(withSidebarState(config, newState)).catch((error) => {
      console.error("Failed to persist sidebar state:", error);
    });
  },
  setSidebarOpen: (open: boolean) => set({ isSidebarOpen: open }),
  homeData: null,
  config: null,
  draftConfig: null,
  enabledMetadataSources: normalizeEnabledMetadataSources(undefined),
  // 初值**不能留空字符串**：界面侧的平台门控写的是正判
  // `platformGOOS === "windows"`（应用内更新、管理员启动、导出快捷启动方式、
  // Locale Emulator / Magpie、批量导入 Steam 等），而这一项由 fetchPlatformGOOS
  // 异步填充 —— 初值为空时这些 Windows 功能会被判成"非 Windows"而隐藏；
  // 若 GetGOOS 调用失败更会永久隐藏。兜底方向取本仓库的历史主平台 Windows，
  // 真值由 GetGOOS 返回后覆盖；Linux 上即使调用失败，最坏也只是多出几个
  // 会优雅报错的 Windows 专属入口。
  platformGOOS: "windows",
  backgroundProcessMuteSupported: false,
  isLoading: false,
  gameRuntimes: {},
  activeGameRuntimeId: "",
  endedGameIds: new Set<string>(),
  librarySelectedTags: [],
  fetchHomeData: async (options = {}) => {
    const showLoading = options.showLoading !== false;
    if (showLoading) {
      set({ isLoading: true });
    }

    try {
      const data = await GetHomePageData();
      set({ homeData: data });
      if (options.syncRuntime !== false) {
        get().setGameRuntimeFromHome(data?.recent_played ?? null);
      }
    }
    catch (error) {
      console.error("Failed to fetch home data:", error);
    }
    finally {
      if (showLoading) {
        set({ isLoading: false });
      }
    }
  },
  fetchConfig: async () => {
    try {
      const loadedConfig = await GetAppConfig();
      const sidebarOpen = get().config
        ? get().isSidebarOpen
        : loadedConfig.sidebar_open;
      const config = withSidebarState(loadedConfig, sidebarOpen);
      set({
        config,
        draftConfig: { ...config },
        enabledMetadataSources: normalizeEnabledMetadataSources(
          config.metadata_sources,
        ),
        isSidebarOpen: sidebarOpen,
      });
    }
    catch (error) {
      console.error("Failed to fetch config:", error);
    }
  },
  fetchPlatformGOOS: async () => {
    try {
      const [goos, backgroundProcessMuteSupported] = await Promise.all([
        GetGOOS(),
        SupportsBackgroundProcessMute(),
      ]);
      // 空串会让上面的正判门控全部 fail-closed，这里保留兜底值。
      set({
        platformGOOS: goos?.trim() || "windows",
        backgroundProcessMuteSupported,
      });
    }
    catch (error) {
      console.error("Failed to fetch platform GOOS:", error);
    }
  },
  applyGameRuntimeEvent: (event: GameRuntimeChangedEvent) => {
    const state = event.state ?? "idle";
    const gameId = event.game_id ?? event.game?.id ?? "";

    if (!gameId) {
      if (state === "idle") {
        set({
          activeGameRuntimeId: "",
          gameRuntimes: {},
        });
      }
      return;
    }

    if (state === "idle") {
      set((currentState) => {
        const currentRuntime = currentState.gameRuntimes[gameId];
        if (
          event.session_id
          && currentRuntime?.sessionId
          && currentRuntime.sessionId !== event.session_id
        ) {
          return currentState;
        }

        const nextGameRuntimes = { ...currentState.gameRuntimes };
        delete nextGameRuntimes[gameId];
        const preferredGameId
          = currentState.activeGameRuntimeId === gameId
            ? ""
            : currentState.activeGameRuntimeId;

        return {
          gameRuntimes: nextGameRuntimes,
          endedGameIds: withEndedGame(currentState.endedGameIds, gameId, true),
          ...runtimeSelectionPatch(nextGameRuntimes, preferredGameId),
        };
      });
      return;
    }

    set((currentState) => {
      const currentRuntime = currentState.gameRuntimes[gameId];
      if (
        isSameSessionStateRegression(currentRuntime, event.session_id, state)
      ) {
        return currentState;
      }
      const nextRuntime: GameRuntimeInfo = {
        activeSeconds:
          typeof event.active_seconds === "number"
            ? event.active_seconds
            : currentRuntime?.activeSeconds,
        game: event.game ?? currentRuntime?.game ?? null,
        gameId,
        isFocused:
          typeof event.is_focused === "boolean"
            ? event.is_focused
            : currentRuntime?.isFocused,
        sessionId: event.session_id ?? currentRuntime?.sessionId ?? "",
        startTime: event.start_time ?? currentRuntime?.startTime ?? null,
        state,
        timingMode: event.timing_mode ?? currentRuntime?.timingMode,
        reason: event.reason ?? currentRuntime?.reason,
        processUnknown: event.process_unknown ?? currentRuntime?.processUnknown,
      };
      const nextGameRuntimes = {
        ...currentState.gameRuntimes,
        [gameId]: nextRuntime,
      };
      const preferredGameId
        = state === "launching"
          || !isGameRuntimeVisible(
            currentState.gameRuntimes[currentState.activeGameRuntimeId],
          )
          ? gameId
          : currentState.activeGameRuntimeId;

      return {
        gameRuntimes: nextGameRuntimes,
        endedGameIds: withEndedGame(currentState.endedGameIds, gameId, false),
        ...runtimeSelectionPatch(nextGameRuntimes, preferredGameId),
      };
    });
  },
  setGameRuntimeFromHome: (recentPlayed: vo.LastPlayedGame[] | null) => {
    const rawPlayingItems = (recentPlayed ?? []).filter(
      item => item.is_playing && item.game?.id,
    );

    set((state) => {
      // 首页快照有延迟：刚结束的游戏在几秒内仍显示 is_playing=true，不能拿它
      // 把已经消失的计时岛重新点亮（见 withEndedGame）。
      const playingItems = rawPlayingItems.filter(
        item => !state.endedGameIds.has(item.game.id),
      );

      if (playingItems.length === 0) {
        if (getVisibleGameRuntimes(state.gameRuntimes).length === 0) {
          return state;
        }

        return {
          gameRuntimes: {},
          ...runtimeSelectionPatch({}, ""),
        };
      }

      const nextGameRuntimes: GameRuntimeMap = {};
      for (const item of playingItems) {
        const game = item.game;
        const currentRuntime = state.gameRuntimes[game.id];
        nextGameRuntimes[game.id] = {
          activeSeconds: currentRuntime?.activeSeconds,
          game,
          gameId: game.id,
          isFocused: currentRuntime?.isFocused,
          sessionId: currentRuntime?.sessionId ?? "",
          startTime: item.last_played_at,
          state:
            currentRuntime?.state === "launching"
            || currentRuntime?.state === "ending"
              ? currentRuntime.state
              : "playing",
          timingMode: currentRuntime?.timingMode,
          reason: currentRuntime?.reason,
          processUnknown: currentRuntime?.processUnknown,
        };
      }

      return {
        gameRuntimes: nextGameRuntimes,
        ...runtimeSelectionPatch(nextGameRuntimes, state.activeGameRuntimeId),
      };
    });
  },
  selectNextGameRuntime: () => {
    set((state) => {
      const runtimes = getVisibleGameRuntimes(state.gameRuntimes);
      if (runtimes.length <= 1) {
        return state;
      }

      const currentIndex = runtimes.findIndex(
        runtime => runtime.gameId === state.activeGameRuntimeId,
      );
      const nextRuntime = runtimes[(currentIndex + 1) % runtimes.length];

      return runtimeSelectionPatch(state.gameRuntimes, nextRuntime.gameId);
    });
  },
  startGame: async (
    game: models.Game,
    options?: Partial<launcher.LaunchOptions>,
  ) => {
    const gameId = game.id;
    if (!gameId) {
      return false;
    }

    const previousGameRuntimes = get().gameRuntimes;
    const previousActiveGameRuntimeId = get().activeGameRuntimeId;
    const previousRuntime = previousGameRuntimes[gameId];
    if (isGameRuntimeVisible(previousRuntime)) {
      set(runtimeSelectionPatch(previousGameRuntimes, gameId));
      return true;
    }

    const optimisticStartTime = new Date().toISOString();
    const optimisticRuntime: GameRuntimeInfo = {
      game,
      gameId,
      sessionId: previousRuntime?.sessionId ?? "",
      startTime: optimisticStartTime,
      state: "launching",
    };
    const optimisticGameRuntimes = {
      ...previousGameRuntimes,
      [gameId]: optimisticRuntime,
    };
    set({
      gameRuntimes: optimisticGameRuntimes,
      ...runtimeSelectionPatch(optimisticGameRuntimes, gameId),
    });

    const rollbackOptimisticRuntime = () => {
      set((state) => {
        const runtime = state.gameRuntimes[gameId];
        if (
          !runtime
          || runtime.startTime !== optimisticStartTime
          || runtime.state !== "launching"
        ) {
          return state;
        }

        const nextGameRuntimes = { ...state.gameRuntimes };
        if (previousRuntime) {
          nextGameRuntimes[gameId] = previousRuntime;
        }
        else {
          delete nextGameRuntimes[gameId];
        }

        return {
          gameRuntimes: nextGameRuntimes,
          ...runtimeSelectionPatch(
            nextGameRuntimes,
            previousActiveGameRuntimeId,
          ),
        };
      });
    };

    try {
      const started = options
        ? await StartGameWithOptions(gameId, options as launcher.LaunchOptions)
        : await StartGameWithTracking(gameId);

      if (!started) {
        rollbackOptimisticRuntime();
      }

      return started;
    }
    catch (error) {
      rollbackOptimisticRuntime();
      throw error;
    }
  },
  patchLiveConfig: async (patch: Partial<appconf.AppConfig>) => {
    const previousConfig = get().config;
    const previousDraftConfig = get().draftConfig;
    const previousEnabledMetadataSources = get().enabledMetadataSources;
    if (!previousConfig) {
      return;
    }

    const nextSidebarOpen
      = typeof patch.sidebar_open === "boolean"
        ? patch.sidebar_open
        : get().isSidebarOpen;
    const nextConfig = withSidebarState(
      { ...previousConfig, ...patch } as appconf.AppConfig,
      nextSidebarOpen,
    );
    const nextDraftConfig = previousDraftConfig
      ? withSidebarState(
          { ...previousDraftConfig, ...patch } as appconf.AppConfig,
          nextSidebarOpen,
        )
      : ({ ...nextConfig } as appconf.AppConfig);

    set({
      config: nextConfig,
      draftConfig: nextDraftConfig,
      enabledMetadataSources: normalizeEnabledMetadataSources(
        nextConfig.metadata_sources,
      ),
      isSidebarOpen: nextSidebarOpen,
    });

    try {
      await UpdateAppConfig(nextConfig);
    }
    catch (error) {
      set({
        config: previousConfig,
        draftConfig: previousDraftConfig,
        enabledMetadataSources: previousEnabledMetadataSources,
        isSidebarOpen: get().isSidebarOpen,
      });
      console.error("Failed to patch live config:", error);
    }
  },
  applyGameLibraryPathChange: async (newPath, syncPaths) => {
    const result = await ApplyGameLibraryPathChange(newPath, syncPaths);
    set(state => ({
      config: state.config
        ? ({
            ...state.config,
            game_library_path: result.new_configured_path,
          } as appconf.AppConfig)
        : null,
      draftConfig: state.draftConfig
        ? ({
            ...state.draftConfig,
            game_library_path: result.new_configured_path,
          } as appconf.AppConfig)
        : null,
    }));
    await get().fetchHomeData({ showLoading: false, syncRuntime: false });
    return result;
  },
  applyCloudSyncStatus: (status: vo.CloudSyncStatus) => {
    set((state) => {
      if (!state.config && !state.draftConfig) {
        return state;
      }

      const patch: Partial<appconf.AppConfig> = {
        last_cloud_sync_time: status.last_sync_time,
        last_cloud_sync_status: status.last_sync_status,
        last_cloud_sync_error: status.last_sync_error,
      };

      return {
        config: state.config
          ? ({ ...state.config, ...patch } as appconf.AppConfig)
          : null,
        draftConfig: state.draftConfig
          ? ({ ...state.draftConfig, ...patch } as appconf.AppConfig)
          : null,
      };
    });
  },
  setDraftConfig: (config: appconf.AppConfig) => {
    set({ draftConfig: config });
  },
  resetDraftConfig: () => {
    const config = get().config;
    const sidebarOpen = get().isSidebarOpen;
    set({
      draftConfig: config
        ? withSidebarState({ ...config } as appconf.AppConfig, sidebarOpen)
        : null,
    });
  },
  saveDraftConfig: async () => {
    const draftConfig = get().draftConfig;
    if (!draftConfig) {
      return;
    }

    const sidebarOpen = get().isSidebarOpen;
    const nextConfig = withSidebarState(
      { ...draftConfig } as appconf.AppConfig,
      sidebarOpen,
    );

    try {
      await UpdateAppConfig(nextConfig);
      set({
        config: nextConfig,
        draftConfig: { ...nextConfig },
        enabledMetadataSources: normalizeEnabledMetadataSources(
          nextConfig.metadata_sources,
        ),
        isSidebarOpen: sidebarOpen,
      });
    }
    catch (error) {
      console.error("Failed to save draft config:", error);
    }
  },
  setLibrarySelectedTags: (tags: string[]) => {
    const nextTags = normalizeLibraryTags(tags);
    if (areStringArraysEqual(get().librarySelectedTags, nextTags)) {
      return;
    }
    set({ librarySelectedTags: nextTags });
  },
  // AI Summary 缓存
  aiSummaryCache: {},
  setAISummary: (dimension: string, summary: string) => {
    set(state => ({
      aiSummaryCache: { ...state.aiSummaryCache, [dimension]: summary },
    }));
  },
  getAISummary: () => {
    return undefined; // 这个方法不需要，直接用 selector 访问
  },
}));
