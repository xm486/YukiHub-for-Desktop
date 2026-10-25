import type { FriendList } from "../../../bindings/yukihub/internal/service/yukihubaccount/models";
import { Link } from "@tanstack/react-router";
import { Browser } from "@wailsio/runtime";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  GetChatUnreadCount,
  ListFriends,
} from "../../../bindings/yukihub/internal/service/accountservice";
import { onWailsEvent } from "../../../src/bindings/runtime";
import galToolboxIconUrl from "../../assets/links/gal-toolbox.png";
import { OPEN_FRIENDS_PANEL_EVENT } from "../../consts/events";
import { EXHIBITION_PATH } from "../../exhibition/constants";
import { useAccountStatus } from "../../hooks/useAccountStatus";
import { useAppStore } from "../../store";
import { SnowflakeMark } from "../branding/SnowflakeMark";
import { FriendsChatModal } from "../modal/FriendsChatModal";

/** 官网社区页（手机版 HomeActivity 的「社区」入口指向同一地址）。 */
const COMMUNITY_URL = "https://yukihub.zh.kg/community";

/** Gal 工具箱（第三方网页工具站，浏览器打开）。 */
const GAL_TOOLBOX_URL = "https://xm486.github.io";

interface SideBarProps {
  bgEnabled?: boolean;
  bgOpacity?: number;
}

export function SideBar({ bgEnabled = false, bgOpacity = 0.85 }: SideBarProps) {
  const { t } = useTranslation();
  const isSidebarOpen = useAppStore(state => state.isSidebarOpen);
  const toggleSidebar = useAppStore(state => state.toggleSidebar);
  const [activeDownloads, setActiveDownloads] = useState(0);
  const accountStatus = useAccountStatus();
  const isLoggedIn = Boolean(accountStatus?.logged_in);
  const [chatUnread, setChatUnread] = useState(0);
  // 待处理好友申请数：收到申请必须能一眼看到（以前只有打开弹窗才知道）
  const [pendingRequests, setPendingRequests] = useState(0);
  const [chatOpen, setChatOpen] = useState(false);

  const [prevSidebarOpen, setPrevSidebarOpen] = useState(isSidebarOpen);
  const [isFading, setIsFading] = useState(false);

  // 在渲染阶段直接更新派生状态，React 官方推荐做法，可避免 Effect 带来的多余重绘并解决 ESLint 警告
  if (isSidebarOpen !== prevSidebarOpen) {
    setPrevSidebarOpen(isSidebarOpen);
    setIsFading(true);
  }

  useEffect(() => {
    if (isFading) {
      const timer = setTimeout(() => {
        setIsFading(false);
      }, 150); // 与 300ms 过渡的一半时间相对应
      return () => clearTimeout(timer);
    }
  }, [isFading]);

  // 监听下载进度事件，统计进行中的任务数
  useEffect(() => {
    const counts: Record<string, string> = {};
    const unsubscribe = onWailsEvent(
      "download:progress",
      (evt: { id: string; status: string }) => {
        counts[evt.id] = evt.status;
        const active = Object.values(counts).filter(
          s => s === "downloading" || s === "pending" || s === "extracting",
        ).length;
        setActiveDownloads(active);
      },
    );
    return unsubscribe;
  }, []);

  // 「展开好友面板」由后端事件驱动：点通知浮层（Steam 同款：点通知打开好友
  // 列表）或别处想直接进好友时，Go 侧广播 friend:open-panel。
  // 通知浮层是另一个窗口里的 React 树，用事件解耦最省事。
  useEffect(
    () => onWailsEvent(OPEN_FRIENDS_PANEL_EVENT, () => setChatOpen(true)),
    [],
  );

  // 聊天未读数：登录后每 15 秒拉一次，并在后端推送好友列表变化时立刻刷（
  // 打开聊天弹窗时不拉，避免覆盖弹窗内的会话已读状态）
  useEffect(() => {
    if (!isLoggedIn || chatOpen) {
      setChatUnread(0);
      return;
    }
    let cancelled = false;
    const refresh = () => {
      GetChatUnreadCount()
        .then((count) => {
          if (!cancelled) {
            setChatUnread(count);
          }
        })
        .catch(() => {
          // 静默：网络抖动不该让侧栏报错
        });
    };
    refresh();

    // 好友申请数：拉一次拿初值，之后靠后端推送更新。
    // 后端推的是**整份好友列表**（含 pendingCount），而它「有实质变化才推」、
    // 变化检测里就含 pendingCount —— 所以有人加我时这里会立刻收到，不用额外轮询。
    ListFriends()
      .then((list) => {
        if (!cancelled) {
          setPendingRequests(list.pendingCount ?? 0);
        }
      })
      .catch(() => {
        // 静默：拿不到就保持原值
      });

    // 15 秒兜底轮询 + 后端好友列表推送时立刻刷一次：
    // 新消息的未读不该等下一个固定周期才出现。
    const timer = window.setInterval(refresh, 15_000);
    const unsubscribe = onWailsEvent<FriendList>(
      "friend:list-updated",
      (list) => {
        if (cancelled) {
          return;
        }
        if (typeof list?.pendingCount === "number") {
          setPendingRequests(list.pendingCount);
        }
        refresh();
      },
    );
    return () => {
      cancelled = true;
      window.clearInterval(timer);
      unsubscribe();
    };
  }, [isLoggedIn, chatOpen]);

  // 弹窗打开时（或未登录）不必显示申请角标：弹窗自己会显示数量
  const pendingBadge = isLoggedIn && !chatOpen ? pendingRequests : 0;

  const navItems = [
    { to: "/", label: t("sideBar.home"), icon: "i-mdi-home" },
    {
      to: "/library",
      label: t("sideBar.library"),
      icon: "i-mdi-gamepad-variant",
    },
    { to: "/stats", label: t("sideBar.stats"), icon: "i-mdi-chart-bar" },
    {
      to: "/categories",
      label: t("sideBar.categories"),
      icon: "i-mdi-format-list-bulleted",
    },
    {
      to: EXHIBITION_PATH,
      label: t("sideBar.exhibition"),
      icon: "i-mdi-view-gallery",
    },
  ];

  const sidebarBgClass = bgEnabled
    ? "border-r border-white/20 dark:border-white/10"
    : "bg-white dark:bg-yh-sidebar border-r border-brand-200 dark:border-brand-700";

  const sidebarStyle = bgEnabled
    ? {
        backgroundColor: `rgba(var(--sidebar-bg-rgb), ${bgOpacity})`,
        transition: "width 300ms ease",
        width: isSidebarOpen ? "16rem" : "4rem",
      }
    : {
        transition: "width 300ms ease",
        width: isSidebarOpen ? "16rem" : "4rem",
      };
  // 选中态对齐手机版 bg_sidebar_item：半透明蓝底 + 左侧强调条，
  // 而不是上游那种"整块实心灰底"。
  const navItemClass
    = "flex items-center rounded-lg border-l-2 border-transparent p-2 text-brand-600 no-underline transition-colors hover:bg-brand-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-400/70 dark:text-brand-300 dark:hover:bg-brand-700/70 [&.active]:border-primary-500 [&.active]:bg-primary-500/12 [&.active]:font-medium [&.active]:text-primary-700 dark:[&.active]:border-primary-300 dark:[&.active]:bg-primary-300/15 dark:[&.active]:text-primary-200 data-glass:hover:bg-white/10 data-glass:hover:dark:bg-black/10 data-glass:[&.active]:bg-white/20 data-glass:[&.active]:dark:bg-black/20";
  /** 展开时显示的文字标签（收起时宽度归零，不会撑开侧栏）。 */
  const renderNavLabel = (label: string) => (
    <div
      className={`overflow-hidden transition-all duration-300 flex items-center ${isSidebarOpen ? "w-[120px] ml-2 opacity-100" : "w-0 ml-0 opacity-0"}`}
    >
      <span className="pointer-events-none whitespace-nowrap shrink-0 break-keep">
        {label}
      </span>
    </div>
  );
  return (
    <aside
      className={`relative z-30 flex shrink-0 flex-col ${sidebarBgClass}`}
      style={sidebarStyle}
    >
      <div
        className={`flex items-center h-16 px-3 ${bgEnabled ? "border-white/20 dark:border-white/10" : "border-brand-200 dark:border-brand-700"}`}
      >
        <div
          className={`flex items-center gap-1 select-none overflow-hidden transition-all duration-300 ${isSidebarOpen ? "opacity-100 max-w-[200px] pl-1" : "opacity-0 max-w-0 pl-0"}`}
        >
          <SnowflakeMark className="h-8 w-8 shrink-0 text-primary-500 dark:text-primary-300" />
          <span className="shrink-0 text-[15px] font-medium tracking-wide text-brand-900 dark:text-brand-100">
            YukiHub
          </span>
        </div>
        <div
          className={`flex-1 flex items-center min-w-[40px] ${isSidebarOpen ? "justify-end" : "justify-center"}`}
        >
          <button
            type="button"
            onClick={toggleSidebar}
            className="shrink-0 rounded-xl p-2 text-brand-700 transition-colors hover:bg-brand-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-400/70 dark:text-brand-300 dark:hover:bg-brand-700 select-none data-glass:hover:bg-white/10 data-glass:hover:dark:bg-black/10"
            aria-label={t("sideBar.toggle")}
            onDragStart={e => e.preventDefault()}
          >
            <div
              className="i-mdi-menu text-xl pointer-events-none"
              aria-hidden="true"
            />
          </button>
        </div>
      </div>

      <nav className="flex-1 py-4">
        <ul className="space-y-2 px-2">
          {navItems.map(item => (
            <li key={item.to}>
              <Link
                to={item.to}
                className={`${navItemClass} select-none`}
                onDragStart={e => e.preventDefault()}
              >
                <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
                  <div
                    className={`${item.icon} text-xl pointer-events-none`}
                    aria-hidden="true"
                  />
                </div>
                {renderNavLabel(item.label)}
              </Link>
            </li>
          ))}

          {/* 好友 / 聊天：独立的大功能，和首页、游戏库同级放在主导航区
              （手机版底部导航同样把聊天当一级入口）。 */}
          <li>
            <button
              type="button"
              onClick={() => setChatOpen(true)}
              aria-label={t("sideBar.chat")}
              className={`${navItemClass} w-full select-none`}
            >
              <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
                <div
                  className="i-mdi-chat-processing-outline text-xl pointer-events-none"
                  aria-hidden="true"
                />
                {chatUnread > 0 && (
                  <span className="absolute -top-1 -right-1 min-w-[16px] h-4 px-1 flex items-center justify-center bg-error-500 text-white text-[10px] font-bold rounded-full leading-none pointer-events-none">
                    {chatUnread > 99 ? "99+" : chatUnread}
                  </span>
                )}
                {/* 好友申请：放右下角，与未读数区分开（两件事互不覆盖） */}
                {pendingBadge > 0 && (
                  <span className="absolute -right-1 -bottom-1 min-w-[16px] h-4 px-1 flex items-center justify-center bg-amber-500 text-white text-[10px] font-bold rounded-full leading-none pointer-events-none">
                    {pendingBadge > 99 ? "99+" : pendingBadge}
                  </span>
                )}
              </div>
              {renderNavLabel(t("sideBar.chat"))}
            </button>
          </li>
        </ul>
      </nav>

      {/* 底部工具栏：**始终竖排**。
          之前这里在展开态切成横排（justify-end gap-1），塞进 4 个入口后直接
          横向溢出把侧栏撑变形 —— 收起态图标横排放不下、展开态更是必然溢出。 */}
      <div
        className={`border-t p-2 ${bgEnabled ? "border-white/20 dark:border-white/10" : "border-brand-200 dark:border-brand-700"}`}
      >
        <div
          className={`flex flex-col gap-2 transition-opacity duration-150 ${isFading ? "opacity-0" : "opacity-100"}`}
        >
          {/* 云同步状态已并入首页用户区（AccountModal），侧栏不再放第二入口 */}

          <Link
            to="/downloads"
            className={`${navItemClass} no-underline select-none data-glass:[&.active]:bg-white/20 data-glass:[&.active]:dark:bg-black/20`}
            onDragStart={e => e.preventDefault()}
          >
            <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
              <div
                className="i-mdi-download text-xl pointer-events-none"
                aria-hidden="true"
              />
              {activeDownloads > 0 && (
                <span className="absolute -top-1 -right-1 min-w-[16px] h-4 px-1 flex items-center justify-center bg-blue-500 text-white text-[10px] font-bold rounded-full leading-none pointer-events-none">
                  {activeDownloads > 99 ? "99+" : activeDownloads}
                </span>
              )}
            </div>
            {renderNavLabel(t("sideBar.downloads"))}
          </Link>

          {/* Gal 工具箱：第三方网页工具站，用自家 logo 图标 */}
          <button
            type="button"
            onClick={() => {
              void Browser.OpenURL(GAL_TOOLBOX_URL).catch((error) => {
                console.error("Failed to open gal toolbox:", error);
              });
            }}
            aria-label={t("sideBar.galToolbox")}
            title={t("sideBar.galToolbox")}
            className={`${navItemClass} select-none`}
          >
            <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
              <img
                src={galToolboxIconUrl}
                alt=""
                draggable={false}
                className="h-5 w-5 rounded object-contain"
              />
            </div>
            {renderNavLabel(t("sideBar.galToolbox"))}
          </button>

          {/* 社区：手机版首页有「社区」按钮（浏览器打开官网社区页） */}
          <button
            type="button"
            onClick={() => {
              void Browser.OpenURL(COMMUNITY_URL).catch((error) => {
                console.error("Failed to open community page:", error);
              });
            }}
            aria-label={t("sideBar.community")}
            title={t("sideBar.community")}
            className={`${navItemClass} select-none`}
          >
            <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
              <div
                className="i-mdi-account-group-outline text-xl pointer-events-none"
                aria-hidden="true"
              />
            </div>
            {renderNavLabel(t("sideBar.community"))}
          </button>

          <Link
            to="/settings"
            className={`${navItemClass} no-underline select-none data-glass:[&.active]:bg-white/20 data-glass:[&.active]:dark:bg-black/20`}
            onDragStart={e => e.preventDefault()}
          >
            <div className="relative shrink-0 flex items-center justify-center w-8 h-8">
              <div
                className="i-mdi-cog text-xl pointer-events-none"
                aria-hidden="true"
              />
            </div>
            {renderNavLabel(t("sideBar.settings"))}
          </Link>
        </div>
      </div>

      <FriendsChatModal
        isOpen={chatOpen}
        isLoggedIn={isLoggedIn}
        onClose={() => setChatOpen(false)}
      />
    </aside>
  );
}
