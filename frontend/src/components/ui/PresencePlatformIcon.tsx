import type { PresencePlatform } from "../../utils/presencePlatform";
import { normalizePresencePlatform } from "../../utils/presencePlatform";

/**
 * 在线状态的「设备平台」线性图标。
 *
 * 契约（docs/yukihub-presence-platform.md §4.1）要求：
 * **线性图标（outline）、24 格 viewBox、圆角线帽**，并且不要用 emoji。
 * 手机版用 vector drawable、网页端用内联 SVG，PC 端这里自己画一套同风格线稿，
 * 三端观感保持一致。
 *
 * 调用方负责「要不要显示」（离线不显示、空值兜底成手机），这里只画图。
 */
const ICON_PATHS: Record<PresencePlatform, string[]> = {
  // 手机：圆角机身 + 底部指示条
  android: [
    "M8 3.25h8a2.25 2.25 0 0 1 2.25 2.25v13a2.25 2.25 0 0 1-2.25 2.25H8a2.25 2.25 0 0 1-2.25-2.25V5.5A2.25 2.25 0 0 1 8 3.25Z",
    "M10.75 18.25h2.5",
  ],
  // 电脑：显示器 + 底座
  pc: [
    "M5 4.75h14A2.25 2.25 0 0 1 21.25 7v6.5A2.25 2.25 0 0 1 19 15.75H5A2.25 2.25 0 0 1 2.75 13.5V7A2.25 2.25 0 0 1 5 4.75Z",
    "M12 15.75v3.5",
    "M8.75 19.25h6.5",
  ],
  // 网页：地球（外圈 + 赤道 + 经线）
  web: [
    "M20.75 12a8.75 8.75 0 1 1-17.5 0 8.75 8.75 0 0 1 17.5 0Z",
    "M3.25 12h17.5",
    "M12 3.25c2.35 2.55 3.6 5.45 3.6 8.75s-1.25 6.2-3.6 8.75c-2.35-2.55-3.6-5.45-3.6-8.75s1.25-6.2 3.6-8.75Z",
  ],
};

interface PresencePlatformIconProps {
  /** 服务端原样下发的值（android / pc / web，也可能为空串或未知值）。 */
  platform?: string | null;
  /** 显示边长（px）。列表项 13 左右，资料页可以稍大一点。 */
  size?: number;
  className?: string;
  /** 悬停提示，一般传本地化后的平台名（手机 / 电脑 / 网页）。 */
  title?: string;
}

export function PresencePlatformIcon({
  platform,
  size = 13,
  className = "",
  title,
}: PresencePlatformIconProps) {
  // 空串 / 未知值一律当手机（与网页端口径一致）
  const resolved = normalizePresencePlatform(platform);
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={title ? undefined : true}
      role={title ? "img" : undefined}
      className={className}
    >
      {title && <title>{title}</title>}
      {ICON_PATHS[resolved].map(path => (
        <path key={path} d={path} />
      ))}
    </svg>
  );
}
