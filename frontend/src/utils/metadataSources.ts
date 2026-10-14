import type { enums } from "../../src/bindings/models";

import { enums as modelEnums } from "../../src/bindings/models";
import bangumiIconUrl from "../assets/providers/bangumi-icon.png";
import bangumiLogoUrl from "../assets/providers/bangumi-logo.png";
import hikarinagiIconUrl from "../assets/providers/hikarinagi-icon.webp";
import hikarinagiLogoUrl from "../assets/providers/hikarinagi-logo.svg";
import nextmoeLogoUrl from "../assets/providers/nextmoe-logo.webp";
import vndbLogoUrl from "../assets/providers/vndb-logo.svg";
import ymgalLogoUrl from "../assets/providers/ymgal-logo.png";

// 与手机版保持一致的可选来源集合。
//
// **不含 bangumi_mirror**：镜像站长期不稳定（bangumi.lol → bangumi.pro 都挂过），
// 手机版已下线该源并用 NextMoe 替代。历史配置里的 `bangumi_mirror` 由 Go 侧
// NormalizeLegacyMetadataSource 静默归一到 bangumi，前端不再把它作为可选项暴露。
export const ALL_METADATA_SOURCES: readonly enums.SourceType[] = [
  modelEnums.SourceType.VNDB,
  modelEnums.SourceType.Bangumi,
  modelEnums.SourceType.Ymgal,
  modelEnums.SourceType.Hikarinagi,
  modelEnums.SourceType.NextMoe,
];

// 默认启用的来源，与 Go 侧 appconf.defaultMetadataSources 一一对应。
export const DEFAULT_ENABLED_METADATA_SOURCES: readonly enums.SourceType[] = [
  modelEnums.SourceType.VNDB,
  modelEnums.SourceType.Bangumi,
  modelEnums.SourceType.Ymgal,
  modelEnums.SourceType.Hikarinagi,
  modelEnums.SourceType.NextMoe,
];

const VALID_METADATA_SOURCE_SET = new Set<string>(ALL_METADATA_SOURCES);

const METADATA_SOURCE_ICONS: Readonly<
  Partial<Record<enums.SourceType, string>>
> = {
  [modelEnums.SourceType.Bangumi]: bangumiLogoUrl,
  [modelEnums.SourceType.VNDB]: vndbLogoUrl,
  [modelEnums.SourceType.Ymgal]: ymgalLogoUrl,
  [modelEnums.SourceType.Hikarinagi]: hikarinagiLogoUrl,
  [modelEnums.SourceType.NextMoe]: nextmoeLogoUrl,
};

const METADATA_SOURCE_COMPACT_ICONS: Readonly<
  Partial<Record<enums.SourceType, string>>
> = {
  ...METADATA_SOURCE_ICONS,
  [modelEnums.SourceType.Bangumi]: bangumiIconUrl,
  [modelEnums.SourceType.Hikarinagi]: hikarinagiIconUrl,
};

/**
 * 该来源的图标是否需要「单色化」（brightness-0 + 暗色模式反白）。
 *
 * vndb / bangumi / ymgal / hikarinagi 提供的是透明底单色 logo，统一打黑
 * （暗色模式反白）才能和界面风格一致；**未萌用的是彩色官方头像**，
 * 套上 brightness-0 会整块变成黑方块（看起来像「图片没加载」），必须原样显示。
 */
export function isMonochromeSourceIcon(source: enums.SourceType): boolean {
  return source !== modelEnums.SourceType.NextMoe;
}

export function getMetadataSourceIcon(
  source: enums.SourceType,
  variant: "logo" | "compact" = "logo",
): string | undefined {
  return variant === "compact"
    ? METADATA_SOURCE_COMPACT_ICONS[source]
    : METADATA_SOURCE_ICONS[source];
}

export function getMetadataSourceURL(
  source: string | undefined,
  sourceId: string | undefined,
): string {
  const id = sourceId?.trim();
  if (!source || !id) {
    return "";
  }

  const encodedId = encodeURIComponent(id);
  switch (source) {
    case "vndb":
      return `https://vndb.org/${encodedId}`;
    // 镜像站与主站条目页一致，使用同一 subject id。
    case "bangumi":
    case "bangumi_mirror":
      return `https://bgm.tv/subject/${encodedId}`;
    case "ymgal":
      return `https://www.ymgal.games/ga/${encodedId}`;
    case "hikarinagi":
      return `https://www.hikarinagi.org/galgames/${encodedId}`;
    // nextmoe 的目录平台尚未开放 Web 前台，没有可跳转的作品页，故不给链接。
    default:
      return "";
  }
}

/**
 * 已下线来源的别名表：老配置 / 老同步快照里可能还存着这些值。
 *
 * 与 Go 侧 appconf.NormalizeLegacyMetadataSource 保持同一张表。
 * 直接判无效会把用户的设置「吃掉」，静默归一更稳妥。
 */
const LEGACY_METADATA_SOURCE_ALIASES: Readonly<Record<string, string>> = {
  bangumi_mirror: "bangumi",
};

export function normalizeEnabledMetadataSources(
  sources: readonly string[] | undefined,
): enums.SourceType[] {
  if (!sources || sources.length === 0) {
    return [...DEFAULT_ENABLED_METADATA_SOURCES];
  }

  const normalized: enums.SourceType[] = [];
  const seen = new Set<string>();
  for (const source of sources) {
    const raw = source.toLowerCase().trim();
    const value = LEGACY_METADATA_SOURCE_ALIASES[raw] ?? raw;
    if (!VALID_METADATA_SOURCE_SET.has(value) || seen.has(value)) {
      continue;
    }
    seen.add(value);
    normalized.push(value as enums.SourceType);
  }

  return normalized.length > 0
    ? normalized
    : [...DEFAULT_ENABLED_METADATA_SOURCES];
}
