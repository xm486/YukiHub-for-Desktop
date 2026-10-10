import type {
  Friend,
  FriendList,
} from "../../../bindings/yukihub/internal/service/yukihubaccount/models";

import { Window } from "@wailsio/runtime";
import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { ListFriends } from "../../../bindings/yukihub/internal/service/accountservice";
import { GetOverlayShortcut } from "../../../bindings/yukihub/internal/service/overlayservice";
import { onWailsEvent } from "../../bindings/runtime";
import { FRIEND_LIST_UPDATED_EVENT } from "../../consts/events";
import {
  presencePlatformLabelKey,
  showsPresencePlatform,
} from "../../utils/presencePlatform";
import { ChatAvatar } from "../chat/ChatAvatar";
import { PresencePlatformIcon } from "../ui/PresencePlatformIcon";
import { OverlayChat } from "./OverlayChat";

interface Sections {
  playing: Friend[];
  online: Friend[];
  offline: Friend[];
}

/** 好友展示名：备注优先（与主界面、通知一致）。 */
function friendDisplayName(friend: Friend): string {
  return friend.note?.trim() || friend.nickname;
}

/**
 * 游戏内好友栏（overlay）—— 按全局快捷键呼出（默认 Shift + ~，可在设置里改），
 * 对应 Steam 的 Shift+Tab。
 *
 * 这是一个**独立的置顶窗口**（URL 走 /overlay），不是主窗口里的浮层：
 * 只有独立窗口才能盖在游戏画面上。窗口由 Go 侧惰性创建（main.go 的
 * toggleOverlayWindow），这里只负责画内容。
 *
 * 点好友 → 在浮层内直接进入私聊（对齐 Steam：overlay 里就能聊天，不必退出游戏）。
 *
 * 数据来源与主界面完全一致：后端每 10 秒轮询一次 /friends/list，有变化就推
 * 整份列表过来，所以浮层里的状态和主界面永远同步。
 *
 * 关于外观：窗口是**实心圆角**（圆角由 Go 侧用 DWM 设置，见 winwindow），
 * 所以这里的根节点直接铺满、不再留边距、也不再自己画圆角与边框 ——
 * 之前留的那圈边距会露出窗口背景，看上去就是「圆角卡片外面还套了一层直角框」。
 *
 * 诚实的限制：能盖在窗口化 / 无边框全屏游戏上，盖不住独占全屏游戏
 * （Steam 靠往游戏进程注入 hook 才做到，YukiHub 不做注入）。
 */
export default function FriendsOverlay() {
  const { t } = useTranslation();
  const [friendList, setFriendList] = useState<FriendList | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [shortcut, setShortcut] = useState("");
  /** 当前正在私聊的好友；null = 停在好友列表 */
  const [activeFriend, setActiveFriend] = useState<Friend | null>(null);

  // 拉一次 + 订阅后端推送：浮层通常开得比第一次轮询早，所以要主动拉一次
  useEffect(() => {
    let cancelled = false;

    ListFriends()
      .then((list) => {
        if (!cancelled) {
          setFriendList(list);
          setError(null);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      });

    const unsubscribe = onWailsEvent<FriendList>(
      FRIEND_LIST_UPDATED_EVENT,
      (list) => {
        if (list?.friends) {
          setFriendList(list);
          setError(null);
        }
      },
    );

    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, []);

  // 底部提示要写实际生效的组合：默认是 Shift + ~，但用户可能改过，
  // 也可能因为冲突退到了备选组合 —— 写死一个按键只会误导。
  useEffect(() => {
    void (async () => {
      try {
        const info = await GetOverlayShortcut();
        setShortcut(info.active_display || info.display || "");
      }
      catch {
        // 拿不到就不显示按键，只留一句「再按一次收起」
      }
    })();
  }, []);

  // Esc：在聊天里退回列表，在列表上收起浮层。
  // 游戏里手不会离开键盘，鼠标点关闭太慢。
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") {
        return;
      }
      if (activeFriend) {
        setActiveFriend(null);
        return;
      }
      void Window.Hide();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [activeFriend]);

  const openChat = (friend: Friend) => {
    setActiveFriend(friend);
    // 聊天要打字，必须让浮层拿到键盘焦点。
    // （列表态刻意不聚焦：只看一眼不该把游戏踢到后台；但打字不可能不聚焦。）
    void Window.Focus();
  };

  // 分组规则与主界面一致（正在游戏 → 在线 → 离线）
  const sections = useMemo<Sections>(() => {
    const result: Sections = { playing: [], online: [], offline: [] };
    for (const friend of friendList?.friends ?? []) {
      if (friend.status === "online" && friend.activity?.trim()) {
        result.playing.push(friend);
      }
      else if (
        friend.status === "online"
        || friend.status === "away"
        || friend.status === "busy"
      ) {
        result.online.push(friend);
      }
      else {
        result.offline.push(friend);
      }
    }
    return result;
  }, [friendList]);

  const hasAnyFriend
    = sections.playing.length + sections.online.length + sections.offline.length
      > 0;

  return (
    // 铺满窗口：圆角由窗口本身提供（Go 侧 DWM），这里不再套第二层卡片
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-brand-900 text-white">
      {/* 顶栏兼拖动柄：overlay 没有标题栏，得给用户一个能拖的地方 */}
      <div
        className="flex shrink-0 items-center justify-between gap-2 border-b border-white/10 px-3 py-2.5"
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      >
        {activeFriend ? (
          <span className="flex min-w-0 items-center gap-1.5">
            <button
              type="button"
              aria-label={t("common.back")}
              // 阻止拖动：按钮在拖动区域里，不拦的话点它会变成拖窗口
              onMouseDown={event => event.stopPropagation()}
              onClick={() => setActiveFriend(null)}
              className="-ml-1 shrink-0 rounded-md p-0.5 text-white/60 transition-colors hover:bg-white/12 hover:text-white"
            >
              <span className="i-mdi-chevron-left text-xl" aria-hidden="true" />
            </button>
            <ChatAvatar
              name={friendDisplayName(activeFriend)}
              avatar={activeFriend.avatar}
              size={22}
            />
            <span className="truncate text-[13px] font-bold">
              {friendDisplayName(activeFriend)}
            </span>
          </span>
        ) : (
          <span className="flex items-center gap-1.5 text-[13px] font-bold">
            <span
              className="i-mdi-account-group-outline text-base text-primary-300"
              aria-hidden="true"
            />
            {t("friendsOverlay.title")}
          </span>
        )}
        <button
          type="button"
          aria-label={t("common.close")}
          onMouseDown={event => event.stopPropagation()}
          onClick={() => void Window.Hide()}
          className="shrink-0 rounded-md p-1 text-white/60 transition-colors hover:bg-white/12 hover:text-white"
        >
          <span className="i-mdi-close text-base" aria-hidden="true" />
        </button>
      </div>

      {activeFriend ? (
        <OverlayChat key={activeFriend.id} friend={activeFriend} />
      ) : (
        <>
          <div className="min-h-0 flex-1 overflow-y-auto px-1.5 py-2">
            {error && (
              <p className="px-2 py-6 text-center text-xs text-white/60">
                {error}
              </p>
            )}
            {!error && !friendList && (
              <p className="px-2 py-6 text-center text-xs text-white/60">
                {t("friendsChat.loading")}
              </p>
            )}
            {!error && friendList && !hasAnyFriend && (
              <p className="px-3 py-6 text-center text-xs whitespace-pre-line text-white/60">
                {t("friendsChat.noFriends")}
              </p>
            )}

            <OverlaySection
              title={t("friendsChat.section.playing")}
              friends={sections.playing}
              highlight
              onSelect={openChat}
            />
            <OverlaySection
              title={t("friendsChat.section.online")}
              friends={sections.online}
              onSelect={openChat}
            />
            <OverlaySection
              title={t("friendsChat.section.offline")}
              friends={sections.offline}
              dim
              onSelect={openChat}
            />
          </div>

          <div className="shrink-0 border-t border-white/10 px-3.5 py-1.5 text-[10px] text-white/40">
            {shortcut
              ? t("friendsOverlay.hint", { shortcut })
              : t("friendsOverlay.hintNoShortcut")}
          </div>
        </>
      )}
    </div>
  );
}

function OverlaySection({
  title,
  friends,
  highlight = false,
  dim = false,
  onSelect,
}: {
  title: string;
  friends: Friend[];
  highlight?: boolean;
  dim?: boolean;
  onSelect: (friend: Friend) => void;
}) {
  const { t } = useTranslation();

  if (friends.length === 0) {
    return null;
  }

  return (
    <div className="mb-1.5">
      <div className="px-2 py-1 text-[10px] font-semibold tracking-wide text-white/45">
        {title}
        {" — "}
        {friends.length}
      </div>
      {friends.map(friend => (
        <button
          key={friend.id || friend.uid}
          type="button"
          onClick={() => onSelect(friend)}
          // 整行可点进私聊：Steam 的 overlay 也是点一下就开始聊
          title={t("friendsOverlay.openChat")}
          className="flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-white/8"
        >
          <div className={`relative shrink-0 ${dim ? "opacity-55" : ""}`}>
            <ChatAvatar
              name={friendDisplayName(friend)}
              avatar={friend.avatar}
              size={32}
            />
            <span
              className={`absolute -right-0.5 -bottom-0.5 h-2.5 w-2.5 rounded-full border-2 border-brand-900 ${
                friend.status === "online"
                  ? "bg-emerald-500"
                  : friend.status === "away" || friend.status === "busy"
                    ? "bg-amber-500"
                    : "bg-brand-400"
              }`}
            />
          </div>
          <div className="min-w-0 flex-1">
            <div
              className={`truncate text-[12px] font-medium ${dim ? "text-white/55" : "text-white/92"}`}
            >
              {friendDisplayName(friend)}
            </div>
            <div
              className={`flex min-w-0 items-center gap-1 text-[10px] ${
                highlight ? "text-emerald-400" : "text-white/50"
              }`}
            >
              {/* 平台图标（手机 / 电脑 / 网页）：离线不显示 */}
              {showsPresencePlatform(friend.status) && (
                <PresencePlatformIcon
                  platform={friend.platform}
                  size={12}
                  className="shrink-0"
                  title={t(presencePlatformLabelKey(friend.platform))}
                />
              )}
              <span className="truncate">{friend.activity?.trim() || ""}</span>
            </div>
          </div>
        </button>
      ))}
    </div>
  );
}
