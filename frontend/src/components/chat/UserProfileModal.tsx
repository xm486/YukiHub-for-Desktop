import type { UserProfile } from "../../../bindings/yukihub/internal/service/yukihubaccount/models";

import { useEffect, useMemo, useState } from "react";
import toast from "react-hot-toast";
import { useTranslation } from "react-i18next";
import {
  GetUserProfile,
  SendFriendRequest,
} from "../../../bindings/yukihub/internal/service/accountservice";
import { useAccountStatus } from "../../hooks/useAccountStatus";
import { presencePlatformLabelKey } from "../../utils/presencePlatform";
import { formatRelativeTime } from "../../utils/relativeTime";
import { formatDuration, formatDurationCompact } from "../../utils/time";
import { ModalPortal } from "../ui/ModalPortal";
import { PresencePlatformIcon } from "../ui/PresencePlatformIcon";
import { ChatAvatar } from "./ChatAvatar";
import { levelBadgeStyle } from "./levelBadge";

interface UserProfileModalProps {
  isOpen: boolean;
  uid: number;
  onClose: () => void;
  /** 点「发消息」时回调；不传就不显示该按钮（群聊里点开成员资料时用） */
  onMessage?: (uid: number) => void;
}

/**
 * 剥掉 activity 的「正在玩：」前缀，只留游戏名；没有前缀就原样返回。
 *
 * 前缀是**固定的中文常量**，不能用当前界面语言去切：activity 由客户端 Go 侧
 * 拼好（AccountService.resolvePlayingActivity 里写死的 "正在玩：" + 游戏名），
 * 英文界面拿到的也还是这个中文串。
 */
function stripActivityPrefix(activity: string): string {
  const prefix = "正在玩：";
  const trimmed = activity.trim();
  return trimmed.startsWith(prefix)
    ? trimmed.slice(prefix.length).trim()
    : trimmed;
}

/**
 * 用户资料弹窗 —— Steam 好友卡的形态。
 *
 * 手机版只有一串纯文本（renderUserProfile 的 recentGames 区块），这里是
 * 结构化重排：正在玩的游戏单独高亮成一条（Steam 好友卡最显眼的元素），
 * 统计与最近游玩各占一块，最近游玩补上「多久之前」。
 *
 * 「已玩多久」依赖服务端的 playingStartedAt（目前还没下发，字段已按别名
 * 列表预留）；没这个字段时只显示游戏名，不做估算。
 */
export function UserProfileModal({
  isOpen,
  uid,
  onClose,
  onMessage,
}: UserProfileModalProps) {
  const { t } = useTranslation();
  const accountStatus = useAccountStatus();
  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [isAddingFriend, setIsAddingFriend] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isOpen || uid <= 0) {
      return;
    }
    let cancelled = false;
    setIsLoading(true);
    setError(null);
    setProfile(null);
    GetUserProfile(uid)
      .then((result) => {
        if (!cancelled) {
          setProfile(result);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setIsLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, uid]);

  const isSelf = Number(accountStatus?.uid ?? 0) === uid;

  /** 加好友（资料页底部按钮，对齐手机版 renderUserProfile 的 actionBar） */
  const handleAddFriend = async () => {
    setIsAddingFriend(true);
    try {
      await SendFriendRequest(String(uid));
      toast.success(t("friendsChat.toastRequestSent"));
      setProfile(previous =>
        previous
          ? { ...previous, friendStatus: "pending", friendDirection: "sent" }
          : previous,
      );
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsAddingFriend(false);
    }
  };

  // 最近游玩按最后游玩时间倒序：服务端给的顺序不保证，拿到的卡片应当是「最近的在上」
  const recentGames = useMemo(() => {
    const list = [...(profile?.recentGames ?? [])];
    return list.sort(
      (first, second) => (second.lastPlayedAt ?? 0) - (first.lastPlayedAt ?? 0),
    );
  }, [profile?.recentGames]);

  if (!isOpen) {
    return null;
  }

  const statusLabel = (() => {
    switch (profile?.status) {
      case "online":
        return t("friendsChat.status.online");
      case "away":
        return t("friendsChat.status.away");
      case "busy":
        return t("friendsChat.status.busy");
      default:
        return t("friendsChat.status.offline");
    }
  })();

  // 服务端目前只给 activity 整串；结构化字段有值时优先用它
  const playingTitle
    = profile?.playingGame || stripActivityPrefix(profile?.activity || "");
  const playingStartedAt = Number(profile?.playingStartedAt ?? 0);

  return (
    <ModalPortal>
      <div
        className="fixed inset-0 z-[70] flex items-center justify-center bg-black/45 p-4 backdrop-blur-sm"
        onClick={onClose}
      >
        <div
          role="dialog"
          aria-modal="true"
          className="w-full max-w-md overflow-hidden rounded-2xl border border-brand-200/90 bg-white shadow-2xl dark:border-brand-700/90 dark:bg-brand-800"
          onClick={event => event.stopPropagation()}
        >
          {/* 顶部横幅：Steam 好友卡用游戏大图做背景，我们没有封面图，用品牌色渐变代替 */}
          <div className="relative h-20 bg-gradient-to-br from-primary-500/85 via-brand-500/70 to-brand-700/70">
            <button
              type="button"
              onClick={onClose}
              aria-label={t("common.close")}
              className="absolute right-2.5 top-2.5 rounded-lg p-1.5 text-white/85 transition-colors hover:bg-white/20 hover:text-white"
            >
              <span className="i-mdi-close text-lg" />
            </button>
          </div>

          <div className="px-4 pb-4">
            {/* 头像压在横幅下沿，是卡片视觉的锚点 */}
            <div className="-mt-10 flex items-end gap-3">
              <div className="rounded-full ring-4 ring-white dark:ring-brand-800">
                <ChatAvatar
                  name={profile?.nickname || `UID ${profile?.uid ?? uid}`}
                  avatar={profile?.avatar}
                  size={80}
                  frame={profile?.frame}
                />
              </div>
              <div className="min-w-0 flex-1 pb-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-lg font-bold text-brand-900 dark:text-white">
                    {profile?.nickname || `UID ${profile?.uid ?? uid}`}
                  </span>
                  {/* 社区等级徽章（手机版资料页在昵称右侧挂 Lv.N） */}
                  {Number(profile?.level ?? 0) > 0 && (
                    <span
                      className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-bold leading-none"
                      style={levelBadgeStyle(Number(profile?.level))}
                    >
                      Lv.
                      {profile?.level}
                    </span>
                  )}
                  {/* 状态徽章：away / busy 也要显示。之前只在 online 时渲染，
                      好友列表里有琥珀点、资料页却什么都不显示，两处对不上 */}
                  {/* 状态行：平台线性图标 + 状态文字。离线不显示（服务端这时
                      也不会下发 platform，返回的是空串） */}
                  {profile?.status && profile.status !== "offline" && (
                    <span
                      className={`inline-flex shrink-0 items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-semibold ${
                        profile.status === "online"
                          ? "bg-success-500/15 text-success-600 dark:text-success-400"
                          : "bg-warning-500/20 text-warning-700 dark:text-warning-400"
                      }`}
                    >
                      <PresencePlatformIcon
                        platform={profile.platform}
                        size={12}
                        className="shrink-0"
                        title={t(presencePlatformLabelKey(profile.platform))}
                      />
                      {statusLabel}
                    </span>
                  )}
                </div>
                <p className="mt-0.5 text-xs text-brand-500 dark:text-brand-400">
                  UID
                  {" "}
                  {profile?.uid ?? uid}
                </p>
              </div>
            </div>

            {isLoading && (
              <p className="py-8 text-center text-sm text-brand-500 dark:text-brand-400">
                {t("friendsChat.loading")}
              </p>
            )}
            {error && (
              <p className="py-8 text-center text-sm text-error-600 dark:text-error-400">
                {error}
              </p>
            )}
            {profile && !isLoading && (
              <div className="mt-3 flex flex-col gap-3">
                {profile.signature && (
                  <p className="whitespace-pre-wrap break-words rounded-lg bg-brand-50 p-3 text-xs leading-relaxed text-brand-700 dark:bg-brand-700/50 dark:text-brand-200">
                    {profile.signature}
                  </p>
                )}

                {/* 正在玩：Steam 好友卡最显眼的一块，独立高亮 */}
                {playingTitle && (
                  <div className="flex items-center gap-2.5 rounded-xl border border-success-500/30 bg-success-500/10 px-3 py-2.5">
                    <span className="i-mdi-controller shrink-0 text-xl text-success-600 dark:text-success-400" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-semibold text-brand-900 dark:text-white">
                        {playingTitle}
                      </p>
                      <p className="text-[11px] text-success-700 dark:text-success-400/90">
                        {/* 服务端还没下发开始时间时不猜：只标「正在玩」 */}
                        {playingStartedAt > 0
                          ? t("friendsChat.playingFor", {
                              duration: formatDuration(
                                Math.max(
                                  0,
                                  Math.floor(
                                    (Date.now() - playingStartedAt) / 1000,
                                  ),
                                ),
                                t,
                              ),
                            })
                          : t("friendsChat.playingNow")}
                      </p>
                    </div>
                  </div>
                )}

                <div className="grid grid-cols-2 gap-3">
                  <div className="rounded-lg border border-brand-200/80 p-3 dark:border-brand-700/80">
                    <div className="flex items-center gap-1 text-[11px] text-brand-500 dark:text-brand-400">
                      <span
                        className="i-mdi-cards-playing-outline"
                        aria-hidden="true"
                      />
                      {t("friendsChat.profileGames")}
                    </div>
                    <div className="mt-0.5 text-sm font-bold text-brand-900 dark:text-white">
                      {profile.totalGames > 0 ? profile.totalGames : "-"}
                    </div>
                  </div>
                  <div className="rounded-lg border border-brand-200/80 p-3 dark:border-brand-700/80">
                    <div className="flex items-center gap-1 text-[11px] text-brand-500 dark:text-brand-400">
                      <span
                        className="i-mdi-clock-outline"
                        aria-hidden="true"
                      />
                      {t("friendsChat.profilePlayTime")}
                    </div>
                    <div className="mt-0.5 text-sm font-bold text-brand-900 dark:text-white">
                      {formatDuration(profile.totalPlayTime, t)}
                    </div>
                  </div>
                </div>

                {/* 最近游玩（对齐手机版 renderUserProfile 的 recentGames 区块） */}
                {recentGames.length > 0 && (
                  <div className="flex flex-col gap-2">
                    <div className="text-[11px] font-semibold text-brand-500 dark:text-brand-400">
                      {t("friendsChat.recentGames")}
                    </div>
                    <div className="flex max-h-52 flex-col divide-y divide-brand-200/70 overflow-auto rounded-lg border border-brand-200/80 scrollbar-hide dark:divide-brand-700/70 dark:border-brand-700/80">
                      {recentGames.map(game => (
                        <div
                          key={game.title}
                          className="flex items-center justify-between gap-3 px-3 py-2"
                        >
                          <span className="min-w-0 flex-1 truncate text-xs text-brand-800 dark:text-brand-100">
                            {game.title}
                          </span>
                          <span className="flex shrink-0 items-center gap-2">
                            {formatRelativeTime(game.lastPlayedAt, t) && (
                              <span className="text-[10px] text-brand-400 dark:text-brand-500">
                                {formatRelativeTime(game.lastPlayedAt, t)}
                              </span>
                            )}
                            <span className="text-[11px] text-brand-500 dark:text-brand-400">
                              {formatDurationCompact(game.playTime, t)}
                            </span>
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {profile.friendSince && (
                  <p className="text-xs text-brand-500 dark:text-brand-400">
                    {t("friendsChat.friendSince", {
                      time: profile.friendSince,
                    })}
                  </p>
                )}

                {/* 底部动作：发消息 + 好友状态（看自己时都不显示） */}
                {!isSelf && (
                  <div className="flex flex-col gap-2 pt-1">
                    {onMessage && (
                      <button
                        type="button"
                        onClick={() => onMessage(uid)}
                        className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-brand-200 py-2 text-sm font-medium text-brand-700 transition-colors hover:bg-brand-100 dark:border-brand-600 dark:text-brand-200 dark:hover:bg-brand-700/60"
                      >
                        <span className="i-mdi-message-text-outline text-base" />
                        {t("friendsChat.profileSendMessage")}
                      </button>
                    )}
                    {profile.friendStatus === "accepted" ? (
                      <div className="flex items-center justify-center gap-1.5 rounded-lg bg-success-500/12 py-2 text-sm font-medium text-success-600 dark:text-success-400">
                        <span className="i-mdi-check text-base" />
                        {t("friendsChat.alreadyFriend")}
                      </div>
                    ) : profile.friendStatus === "pending" ? (
                      <div className="rounded-lg bg-brand-100/80 py-2 text-center text-xs text-brand-500 dark:bg-brand-700/50 dark:text-brand-400">
                        {profile.friendDirection === "received"
                          ? t("friendsChat.friendRequestReceived")
                          : t("friendsChat.friendRequestSent")}
                      </div>
                    ) : (
                      <button
                        type="button"
                        onClick={() => void handleAddFriend()}
                        disabled={isAddingFriend}
                        className="flex w-full items-center justify-center gap-1.5 rounded-lg bg-primary-500 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-600 disabled:opacity-60"
                      >
                        <span
                          className={`text-base ${isAddingFriend ? "i-mdi-loading animate-spin" : "i-mdi-account-plus-outline"}`}
                        />
                        {t("friendsChat.addFriend")}
                      </button>
                    )}
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}
