/**
 * 在线状态的「设备平台标识」工具。
 *
 * 契约见 `docs/yukihub-presence-platform.md`（服务端 → 客户端）：
 * - 服务端只下发 `android` / `pc` / `web` 三个小写值；
 * - 对方**离线**时个人主页接口返回空串（离线时展示平台没意义），
 *   好友列表因为只列在线用户，总是有值；
 * - 展示时客户端自己做一次兜底：**空串或未知值一律当手机**（与网页端口径一致）。
 *
 * 这个字段纯粹是展示用的设备标识，**不要拿它做任何业务判断**。
 */

export type PresencePlatform = "android" | "pc" | "web";

/** 空串 / 未知值一律当手机。 */
export function normalizePresencePlatform(
  raw?: string | null,
): PresencePlatform {
  const value = (raw ?? "").trim().toLowerCase();
  if (value === "pc" || value === "web") {
    return value;
  }
  return "android";
}

/**
 * 是否应当显示平台图标。
 *
 * 契约要求「用户离线 → 不显示平台图标」，而 PC 端好友列表里**离线好友也在列表里**，
 * 所以不能只看 platform 有没有值，还要看在线状态。
 * 状态取值参考服务端语义：online / away / busy / offline（空串视为未知＝离线）。
 */
export function showsPresencePlatform(status?: string | null): boolean {
  const value = (status ?? "").trim().toLowerCase();
  return value === "online" || value === "away" || value === "busy";
}

/** i18n 键（`friendsChat.platform.<平台>`），用来做图标悬停提示。 */
export function presencePlatformLabelKey(raw?: string | null): string {
  return `friendsChat.platform.${normalizePresencePlatform(raw)}`;
}
