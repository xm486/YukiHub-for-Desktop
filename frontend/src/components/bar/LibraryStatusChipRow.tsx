import type { GameStatusFilter, HiddenFilter } from "../../consts/options";
import { useTranslation } from "react-i18next";
import { GAME_STATUS_BADGE_STYLES } from "../../consts/gameStatusBadge";
import { statusOptions } from "../../consts/options";

interface LibraryStatusChipRowProps {
  statusFilter: GameStatusFilter;
  onStatusFilterChange: (value: GameStatusFilter) => void;
  /** 隐藏游戏的过滤档位（`hide` / `only`），见 consts/options.ts 的说明 */
  hiddenFilter: HiddenFilter;
  onHiddenFilterChange: (value: HiddenFilter) => void;
}

/**
 * 手机版游戏库顶部状态筛选芯片行（PC 版横向平铺，对齐 bg_chip）。
 *
 * 整行是**单选**的一组分类，最后一个「隐藏」就是手机版筛选栏里的「隐藏」分类：
 *   - 点状态芯片 → 状态筛选生效，**同时退出隐藏分类**；
 *   - 点「隐藏」→ 清掉状态筛选，只看已隐藏的游戏（误隐藏想找回时唯一的入口）；
 *   - 「隐藏」再点一次 → 退回默认（隐藏的游戏不进列表）。
 *
 * 互斥逻辑放在这里而不是让父级拼两个回调：这一行本身就是「一组互斥分类」，
 * 把「离开隐藏分类」「进隐藏分类要清状态」这两条规则散到父级，
 * 以后加第四个入口时必然漏掉其中一条。父级只管持有 state。
 */
export function LibraryStatusChipRow({
  statusFilter,
  onStatusFilterChange,
  hiddenFilter,
  onHiddenFilterChange,
}: LibraryStatusChipRowProps) {
  const { t } = useTranslation();
  const onlyHidden = hiddenFilter === "only";

  return (
    <div className="-mx-1 flex items-center gap-2 overflow-x-auto px-1 py-1">
      {statusOptions.map((option) => {
        // 处于「只看隐藏」时，状态芯片一律不算选中 —— 列表里此时只有隐藏游戏，
        // 继续高亮「未玩」会让人以为筛选还生效着。
        const active
          = !onlyHidden && (statusFilter || "") === option.value;
        const badge = option.value
          ? GAME_STATUS_BADGE_STYLES[option.value]
          : undefined;
        return (
          <button
            key={`status-chip-${option.value || "all"}`}
            type="button"
            aria-pressed={active}
            onClick={() => {
              onStatusFilterChange(option.value);
              onHiddenFilterChange("hide");
            }}
            className={
              active
                ? "yh-primary-pill inline-flex shrink-0 items-center gap-1.5 rounded-full px-3.5 py-1.5 text-xs font-bold text-white shadow-md transition-transform active:scale-95"
                : "yh-chip shrink-0 cursor-pointer px-3.5 py-1.5 text-xs font-bold transition-colors hover:border-primary-300 hover:bg-white dark:hover:bg-[#22314A]"
            }
          >
            {badge && (
              <span className={`${badge.icon} text-sm`} aria-hidden="true" />
            )}
            {t(option.label)}
          </button>
        );
      })}

      <button
        type="button"
        aria-pressed={onlyHidden}
        onClick={() => {
          if (onlyHidden) {
            // 再点一次退回默认：只切档位，不动状态筛选（本来就已经被清空了）
            onHiddenFilterChange("hide");
            return;
          }
          onStatusFilterChange("");
          onHiddenFilterChange("only");
        }}
        className={
          onlyHidden
            ? "yh-primary-pill inline-flex shrink-0 items-center gap-1.5 rounded-full px-3.5 py-1.5 text-xs font-bold text-white shadow-md transition-transform active:scale-95"
            : "yh-chip shrink-0 cursor-pointer px-3.5 py-1.5 text-xs font-bold transition-colors hover:border-primary-300 hover:bg-white dark:hover:bg-[#22314A]"
        }
      >
        <span
          className={
            onlyHidden
              ? "i-mdi-eye-outline text-sm"
              : "i-mdi-eye-off-outline text-sm"
          }
          aria-hidden="true"
        />
        {t("filterBar.hiddenCategory")}
      </button>
    </div>
  );
}
