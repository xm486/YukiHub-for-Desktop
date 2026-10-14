/**
 * 好友列表的排序与分区（对齐手机版 FriendsChatDialog 与网页端 friends/list.php）。
 *
 * 排序键是四级复合键，两端口径必须一致：
 *
 *   ① 有未读消息的置顶 —— 好友可能已经离线好几天，但消息没看；
 *      只按在线状态排的话它会被埋进「离线」区，用户根本注意不到。
 *   ② 在线状态：正在游戏 → 在线/忙碌 → 离开/离线
 *   ③ 同组内按最后上线时间倒序，缺失/解析不出来的排最后
 *   ④ 仍相同则按显示名，保证顺序稳定（否则 React 每次重排都会跳）
 *
 * 主界面（FriendsChatModal）与游戏内浮层（FriendsOverlay）共用这一份：
 * 两处各写一份的话，同一份数据会在两个界面里呈现出不同的顺序。
 *
 * 这里刻意不 import bindings 的类型，改用结构类型：工具因此不依赖生成产物，
 * 也顺手让「排序只读这几个字段」这件事在签名上就写清楚。
 */

import type { TFunction } from "i18next";

import { formatRelativeTime } from "./relativeTime";

/** 排序与分区实际会读到的字段。 */
export type SortableFriend = {
  id?: string;
  nickname?: string;
  note?: string;
  /** online / away / busy / offline */
  status?: string;
  /** 「正在玩：xxx」，对方关闭分享时为空 */
  activity?: string;
  unreadCount?: number;
  /** 服务端下发的最后心跳时间，形如 "2026-05-31 12:00:00" */
  lastHeartbeat?: string;
};

/** 分区键，数组顺序即渲染顺序（对齐手机版：未读 → 正在游戏 → 在线 → 离线）。 */
export const FRIEND_SECTION_KEYS = [
  "unread",
  "playing",
  "online",
  "offline",
] as const;

export type FriendSectionKey = (typeof FRIEND_SECTION_KEYS)[number];

export type FriendSections<T extends SortableFriend> = Record<
  FriendSectionKey,
  T[]
>;

/** 好友展示名：备注优先（与主界面、通知一致）。 */
export function friendDisplayName(friend: SortableFriend): string {
  return friend.note?.trim() || friend.nickname?.trim() || "";
}

/**
 * 在线状态分区权重，数字小的排前面。
 *
 * 与手机版 friendGroupRank 逐条对应，注意两点容易写错的地方：
 *   - 只有「在线 **且** 有正在玩的游戏」才算第 0 档；
 *   - **away（离开）落在最后一档**，不是「在线」那一档。
 */
export function friendGroupRank(friend: SortableFriend): number {
  if (friend.status === "online" && friend.activity?.trim()) {
    return 0;
  }
  if (friend.status === "online" || friend.status === "busy") {
    return 1;
  }
  return 2;
}

/**
 * 把 lastHeartbeat 解析成毫秒时间戳；缺失或解析失败一律返回 0（排最后）。
 *
 * 服务端下发的是本地时间串 `yyyy-MM-dd HH:mm:ss`；这里额外容忍 ISO 8601
 * （带 T、带毫秒、带时区），免得服务端哪天换成 ISO 就把整列排序打乱。
 * 注意 `Date.parse("2026-05-31 12:00:00")` 在 Safari/旧引擎上会返回 NaN，
 * 所以格式固定的那种先手工换成 `T` 再解析。
 *
 * **不要**指望这两种格式能互相比较：无时区的串按本机时区解释、带 `Z` 的按 UTC，
 * 同一条流水线上混用就会差出一个时区。服务端只用一种格式，所以相对顺序是对的；
 * 真要换格式得整体换，不能一半一半。
 */
export function parseFriendHeartbeat(value: string | null | undefined): number {
  const raw = value?.trim();
  if (!raw) {
    return 0;
  }
  const normalized = /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(raw)
    ? raw.replace(" ", "T")
    : raw;
  const parsed = Date.parse(normalized);
  return Number.isNaN(parsed) ? 0 : parsed;
}

/**
 * 「最后在线 X」文案（离线/离开的好友），对齐手机版
 * FriendsChatDialog.formatHeartbeatRelative → formatRelativeTime。
 *
 * 同一份心跳字段有两个用途：**排序**用 parseFriendHeartbeat，
 * **展示**用它。两者都走 parseFriendHeartbeat 解析，保证「按什么排」
 * 和「显示什么」不会互相矛盾（手机端注释也专门强调了这点）。
 *
 * 解析不出来一律返回空串，调用方据此回退到签名/状态文案。
 */
export function formatFriendLastSeen(
  value: string | null | undefined,
  t: TFunction,
  now: number = Date.now(),
): string {
  return formatRelativeTime(parseFriendHeartbeat(value), t, now);
}

/**
 * 按四级复合键排序好友列表。
 *
 * 心跳解析放在比较器外面预解析：比较器会被调用 O(n log n) 次，
 * 而列表渲染可能在轮询回调里触发，重复解析时间字符串会造成掉帧
 * （手机版也是先建一张缓存表再排）。
 */export function sortFriends<T extends SortableFriend>(
  friends: readonly T[],
): T[] {
  const heartbeats = new Map<T, number>();
  for (const friend of friends) {
    heartbeats.set(friend, parseFriendHeartbeat(friend.lastHeartbeat));
  }

  return [...friends].sort((a, b) => {
    const unreadA = (a.unreadCount ?? 0) > 0 ? 0 : 1;
    const unreadB = (b.unreadCount ?? 0) > 0 ? 0 : 1;
    if (unreadA !== unreadB) {
      return unreadA - unreadB;
    }

    const rankDiff = friendGroupRank(a) - friendGroupRank(b);
    if (rankDiff !== 0) {
      return rankDiff;
    }

    const timeDiff = (heartbeats.get(b) ?? 0) - (heartbeats.get(a) ?? 0);
    if (timeDiff !== 0) {
      return timeDiff;
    }

    // 与手机版 compareToIgnoreCase 等价；不用 localeCompare，
    // 免得同一份数据在不同系统语言下排出不同顺序（那会让两端看起来不一致）。
    const nameA = friendDisplayName(a).toLowerCase();
    const nameB = friendDisplayName(b).toLowerCase();
    if (nameA === nameB) {
      return 0;
    }
    return nameA < nameB ? -1 : 1;
  });
}

/**
 * 排序后切分成四个分区。
 *
 * 有未读的会**从状态分区里摘出去**单独成一区，这和手机版一致：
 * 同一个好友不能同时出现在「未读消息」和「离线」两处。
 */
export function sectionFriends<T extends SortableFriend>(
  friends: readonly T[],
): FriendSections<T> {
  const sections: FriendSections<T> = {
    unread: [],
    playing: [],
    online: [],
    offline: [],
  };

  for (const friend of sortFriends(friends)) {
    if ((friend.unreadCount ?? 0) > 0) {
      sections.unread.push(friend);
      continue;
    }
    const rank = friendGroupRank(friend);
    if (rank === 0) {
      sections.playing.push(friend);
    }
    else if (rank === 1) {
      sections.online.push(friend);
    }
    else {
      sections.offline.push(friend);
    }
  }

  return sections;
}
