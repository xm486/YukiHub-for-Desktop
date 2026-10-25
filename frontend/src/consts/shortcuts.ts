/**
 * 全局快捷键表。
 *
 * 约定：**一律带 Ctrl/Cmd + Shift**，避免与输入框、浏览器默认行为打架
 * （缩放是 Ctrl+±/0，单独占用一套）。`key` 用 `event.key` 的小写形式比较。
 */
import { EXHIBITION_PATH } from "../exhibition/constants";

export type GlobalShortcut = {
  /** 稳定标识，同时用作 i18n 键的一部分 */
  id: string;
  /** 展示用的按键名，同时是 `event.key` 的小写形式 */
  key: string;
  /**
   * 按 `event.code` 匹配，用于 Shift 会改变字符的键。
   *
   * 例：美式键盘上 Shift+/ 的 `event.key` 是 `?`，用 code 才能稳定命中。
   */
  code?: string;
  /** 导航目标；为空表示触发快捷键速查弹窗 */
  path?: string;
};

/** 打开快捷键速查弹窗的自定义事件名。 */
export const SHORTCUT_DIALOG_EVENT = "yukihub:show-shortcuts";

export const GLOBAL_SHORTCUTS: GlobalShortcut[] = [
  { id: "home", key: "h", path: "/" },
  { id: "library", key: "l", path: "/library" },
  { id: "stats", key: "s", path: "/stats" },
  // 分类页在侧栏叫「收藏」，用 f；c 留给 Chromium 的检查元素
  { id: "categories", key: "f", path: "/categories" },
  { id: "downloads", key: "d", path: "/downloads" },
  { id: "settings", key: "p", path: "/settings" },
  { id: "bigscreen", key: "b", path: "/bigscreen" },
  // 展厅与其余一级页面同等：侧栏图标在收起态只有图形，给它配一个直达键
  { id: "exhibition", key: "e", path: EXHIBITION_PATH },
  // 不导航，只弹速查表；放在最后，速查弹窗里也列出来
  { id: "cheatsheet", key: "/", code: "Slash" },
];
