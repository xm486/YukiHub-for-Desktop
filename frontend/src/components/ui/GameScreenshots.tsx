import { useEffect, useState } from "react";
import { GetGameScreenshots } from "../../../bindings/yukihub/internal/service/gameservice";
import { useAppStore } from "../../store";
import { ProxyImage } from "./ProxyImage";

interface GameScreenshotsProps {
  gameId: string;
  isNSFW?: boolean;
  /** 最多显示几张；手机端游戏库详情面板是 2 个固定位 */
  limit?: number;
  /** 小节标题，传了才渲染（没有截图时标题与图片一起收起） */
  label?: string;
  labelClassName?: string;
  rowClassName?: string;
  itemClassName?: string;
}

/**
 * 游戏截图行，对齐手机端**游戏库详情面板**的「媒体」小节：
 * `activity_main.xml` 的 `detailPanel` 在「标签」之后是
 * `HorizontalScrollView → sideScreenshot1 / sideScreenshot2`（64dp×42dp、centerCrop），
 * 数据由 `MetadataController.applyVndbMetadata` 从元数据缓存的 `screenshotUrls`
 * 取前两张填进去。
 *
 * - 与封面同规则：NSFW 且开启「模糊 NSFW 封面」时**整块不渲染**（连请求都不发）
 * - 单张加载失败隐藏该张；一张都没出来时整块（含标题）收起
 * - 截图数据由 `GetGameScreenshots` 按手机端 BigScreenMeta 的来源优先级取第一组非空
 */
export function GameScreenshots({
  gameId,
  isNSFW = false,
  limit = 2,
  label,
  labelClassName = "text-[11px] font-semibold text-brand-500 dark:text-white/60",
  rowClassName = "mt-1.5 flex gap-2",
  itemClassName = "aspect-[3/2] min-w-0 grow basis-0 overflow-hidden rounded-lg bg-brand-200 dark:bg-brand-900/60",
}: GameScreenshotsProps) {
  const shouldBlurNSFW = useAppStore(
    state => state.config?.blur_nsfw_game_covers !== false,
  );
  const hidden = isNSFW && shouldBlurNSFW;
  const [urls, setUrls] = useState<string[]>([]);
  const [failed, setFailed] = useState<string[]>([]);

  useEffect(() => {
    if (hidden) {
      return;
    }
    let cancelled = false;
    void GetGameScreenshots(gameId)
      .then((list) => {
        if (cancelled) {
          return;
        }
        setUrls(
          Array.isArray(list)
            ? list.filter(url => url.trim().length > 0)
            : [],
        );
      })
      .catch((error) => {
        // 截图是锦上添花：读不到就整块不显示，不打扰用户
        console.error("Failed to load game screenshots:", error);
        if (!cancelled) {
          setUrls([]);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [gameId, hidden]);

  if (hidden) {
    return null;
  }

  const visible = urls.filter(url => !failed.includes(url)).slice(0, limit);
  if (visible.length === 0) {
    return null;
  }

  return (
    <div className="min-w-0 shrink-0">
      {label ? <p className={labelClassName}>{label}</p> : null}
      <div className={rowClassName}>
        {visible.map(url => (
          <div key={url} className={itemClassName}>
            <ProxyImage
              src={url}
              isNSFW={isNSFW}
              alt=""
              className="h-full w-full object-cover"
              decoding="async"
              onError={() =>
                setFailed(current =>
                  current.includes(url) ? current : [...current, url],
                )}
            />
          </div>
        ))}
      </div>
    </div>
  );
}
