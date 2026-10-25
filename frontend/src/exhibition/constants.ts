/**
 * 展厅（离线）相关常量。
 *
 * 单独放一个文件是为了避免 `routes/__root.tsx` ↔ `routes/exhibition.tsx`
 * 互相 import 形成环 —— 与大屏的 `bigscreen/constants.ts` 同一做法。
 */

/** 展厅路由（同窗口的无外壳全屏路由，和 /bigscreen 同类）。 */
export const EXHIBITION_PATH = "/exhibition";

/**
 * 展厅页面入口。
 *
 * 这里是手机版 `assets/exhibition/` 的原样移植，放在 `frontend/public/exhibition-hall/`，
 * 构建时由 Vite 原封不动复制到 `dist/exhibition-hall/`，再由 Wails 的 asset server
 * 同源提供 —— 只有同源才能让 iframe 读到父窗口注入的桥接对象。
 */
export const HALL_PAGE_PATH = "/exhibition-hall/index.html";
