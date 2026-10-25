import { createRoute, useRouter } from "@tanstack/react-router";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "react-hot-toast";
import { useTranslation } from "react-i18next";
import { GetLibraryJSON } from "../../bindings/yukihub/internal/service/exhibitionservice";
import { EXHIBITION_PATH, HALL_PAGE_PATH } from "../exhibition/constants";
import { useAppStore } from "../store";
import { Route as rootRoute } from "./__root";

/**
 * 展厅页面期望的桥接形状 —— 与手机版 `ExhibitionBridge` 一一对应。
 *
 * 为什么是"同步返回值"：手机版是 Android `@JavascriptInterface`，页面里写的是
 * `JSON.parse(bridge.getMyLibrary())`，没有任何 await。桌面端 Wails 绑定是异步的，
 * 所以这里做的是「先用异步绑定把数据取回来，再用一个同步函数把字符串交出去」。
 */
interface ExhibitionBridge {
  onReady: (stage: string) => void;
  onPerf: (json: string) => void;
  log: (msg: string) => void;
  toast: (msg: string) => void;
  getMyLibrary: () => string;
  getMusicLibrary: () => string;
}

/**
 * 桌面端还没有音乐库（手机版的 MusicRepository 是 Android 独有的）。
 *
 * 返回空集而不是不实现这个方法：music.js 在"桥接缺失"时是直接 return，
 * 连引导提示都不会出现；给空集才会走到 onLibrary([])，页面显示
 * 「音乐厅还没有专辑」，玩家知道这里本该有东西。
 */
const EMPTY_MUSIC_LIBRARY = JSON.stringify({
  albums: [],
  tracks: [],
  count: 0,
  trackCount: 0,
});

/** 挂在 window 上、供同源 iframe 读取的宿主侧对象。 */
type ExhibitionHostWindow = Window & {
  __yukihubExhibitionBridge?: ExhibitionBridge;
  __yukihubExhibitionBlurNsfw?: boolean;
};

/**
 * 展厅页面的诊断日志。
 *
 * 只在开发构建里输出：页面自己会在 HUD 上显示帧率与封面队列，真正的失败
 * （WebGL 起不来）会由页面内的错误覆盖层直接告诉用户，生产环境没有必再刷屏。
 * 用 warn 是因为仓库的 `no-console` 只放行 warn / error。
 */
const debugLog = import.meta.env.DEV
  ? (message: string) => console.warn(`[exhibition] ${message}`)
  : () => {};

function isExitMessage(data: unknown): boolean {
  return typeof data === "object"
    && data !== null
    && (data as { type?: unknown }).type === "yukihub-exhibition-exit";
}

function ExhibitionPage() {
  const { t } = useTranslation();
  const router = useRouter();
  // 与 App 设置保持一致：默认打码（与 ProxyImage 的 `!== false` 同口径）
  const blurNsfw = useAppStore(
    state => state.config?.blur_nsfw_game_covers !== false,
  );

  const [libraryJSON, setLibraryJSON] = useState<string | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    GetLibraryJSON()
      .then((raw) => {
        if (!cancelled) {
          setLibraryJSON(raw);
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          setLoadError(error instanceof Error ? error.message : String(error));
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const handleExit = useCallback(() => {
    // 从哪来回哪去：展厅是"走进另一间屋子"，不该把用户丢回首页
    if (window.history.length > 1) {
      router.history.back();
      return;
    }
    void router.navigate({ to: "/" });
  }, [router]);

  // 页面里按 Esc / 点「← 返回」时通过 postMessage 请求退出。
  // 必须校验 origin：同源才认，避免任意页面把我们导走。
  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin) {
        return;
      }
      if (isExitMessage(event.data)) {
        handleExit();
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [handleExit]);

  // ⚠️ 桥接必须在**渲染 iframe 之前**就位。
  //    iframe 的文档一创建，index.html 里的内联脚本就会同步读取
  //    parent.__yukihubExhibitionBridge；晚一拍（比如放进 useEffect）
  //    页面就会退化成"桥接不可用：离线展厅需要在 YukiHub App 内打开"。
  //    因此这里在渲染期（useMemo）赋值 —— 它是我们自己的全局对象，幂等且确定。
  const bridge = useMemo<ExhibitionBridge | null>(() => {
    if (libraryJSON === null) {
      return null;
    }
    const host = window as ExhibitionHostWindow;
    const value: ExhibitionBridge = {
      onReady: (stage) => {
        debugLog(`页面就绪：${stage}`);
      },
      onPerf: (json) => {
        // 每 5 秒一条性能摘要，只有开着展厅时才会出现
        debugLog(`性能：${json}`);
      },
      log: (msg) => {
        debugLog(msg);
      },
      toast: (msg) => {
        toast(msg);
      },
      getMyLibrary: () => libraryJSON,
      getMusicLibrary: () => EMPTY_MUSIC_LIBRARY,
    };
    host.__yukihubExhibitionBridge = value;
    host.__yukihubExhibitionBlurNsfw = blurNsfw;
    return value;
  }, [libraryJSON, blurNsfw]);

  // 离开路由时清掉，别把整份库存 JSON 留在 window 上
  useEffect(() => {
    return () => {
      const host = window as ExhibitionHostWindow;
      delete host.__yukihubExhibitionBridge;
      delete host.__yukihubExhibitionBlurNsfw;
    };
  }, []);

  if (loadError !== null) {
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center gap-4 bg-[#05070C] px-8 text-center">
        <h2 className="text-base font-semibold text-amber-300">
          {t("exhibition.loadFailedTitle")}
        </h2>
        <p className="max-w-xl text-sm leading-relaxed text-slate-400">
          {t("exhibition.loadFailedDesc", { message: loadError })}
        </p>
        <button
          type="button"
          onClick={handleExit}
          className="rounded-lg border border-white/20 bg-white/10 px-4 py-2 text-sm text-slate-100 transition-colors hover:bg-white/20"
        >
          {t("exhibition.back")}
        </button>
      </div>
    );
  }

  if (bridge === null) {
    return (
      <div className="flex h-screen w-full flex-col items-center justify-center gap-3 bg-[#05070C]">
        <span className="text-sm text-slate-400">{t("exhibition.loading")}</span>
        <button
          type="button"
          onClick={handleExit}
          className="text-xs text-slate-500 underline-offset-4 hover:underline"
        >
          {t("exhibition.back")}
        </button>
      </div>
    );
  }

  return (
    <div className="h-screen w-full bg-[#05070C]">
      {/*
        刻意不加 sandbox：页面必须与本页面**同源**才能读到
        parent.__yukihubExhibitionBridge（不透明 origin 下会直接抛异常）；
        而 sandbox 同时给 allow-scripts + allow-same-origin 等于没有限制，
        加个壳只会让人误以为这里有隔离。
      */}
      {/* eslint-disable-next-line react-dom/no-missing-iframe-sandbox */}
      <iframe
        src={HALL_PAGE_PATH}
        title={t("exhibition.title")}
        // 只放行自动播放、全屏与手柄（gamepad.js 会用 navigator.getGamepads）
        allow="autoplay; fullscreen; gamepad"
        className="h-full w-full border-0"
      />
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: EXHIBITION_PATH,
  component: ExhibitionPage,
});
