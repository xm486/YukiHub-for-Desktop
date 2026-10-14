import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { presetWind3 } from "unocss";

/**
 * UnoCSS 静默失效守卫。
 *
 * 为什么需要它：UnoCSS 遇到「色板里没有的色阶」「主题里没有的动画名」「主题里没有的
 * 尺寸档位」时 **不报错、不告警，直接不生成任何 CSS**，构建依然成功。踩过三次：
 *
 * - `bg-brand-950`（色板只到 900）→ 大屏入场层/详情层/设置层的遮罩全部变透明，
 *   表现是「启动动画没有遮罩，背后的游戏列表直接可见」；
 * - `animate-spin-slow`（主题里没有这个动画）→ 设置页的加载齿轮不转；
 * - `max-w-8xl`（presetWind3 只到 7xl）→ 9 个页面/骨架的宽度上限全是空转，
 *   超宽屏上内容被拉满，骨架和真实页面也永远对不齐。
 *
 * 本脚本只做**零误报**的检查，只看「引用了不存在的令牌」，与文件是否被打进产物无关，
 * 所以不会误伤死代码：
 *   1. `(前缀)-(自定义色板)-(色阶)` 里的色阶必须在 uno.config.ts 的 theme.colors 里存在；
 *   2. `animate-<名字>` 必须是主题里声明过的动画，或 presetWind 自带的那几个；
 *   3. `(max-w|min-w|max-h|min-h)-<名字>` 的名字必须来自 presetWind3 的尺寸主题、
 *      本仓库 theme 的补充，或 CSS 尺寸关键字；纯数字与任意值（`max-w-[86vh]`）
 *      交给 spacing / bracket 解析，不参与判断；
 *   4. `i-<集合>-<图标名>` 必须在已安装的 `@iconify-json/<集合>` 里真实存在 ——
 *      图标写错同样是**静默失效**：类名不生成任何 CSS，按钮上就是一片空白，
 *      连 hover 都没有提示，比色阶错了更难发现。
 */

const frontendRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const sourceDirectory = join(frontendRoot, "src");
const configPath = join(frontendRoot, "uno.config.ts");

/** presetWind3 自带的动画名，不需要在主题里声明 */
const BUILTIN_ANIMATIONS = new Set(["none", "spin", "ping", "pulse", "bounce"]);

/**
 * presetWind3 用代码而不是主题表处理的尺寸关键字（`min-h-full`、`max-w-screen` …），
 * 所以它们不在 theme.width / theme.maxWidth 里，必须单独放行。
 */
const SIZE_KEYWORDS = new Set([
  "auto",
  "full",
  "screen",
  "min",
  "max",
  "fit",
  "none",
  "prose",
  "px",
  "rem",
  "em",
  "vw",
  "vh",
  "dvh",
  "svh",
  "lvh",
  "ch",
  "ex",
]);

/** 提取 theme.maxWidth / minWidth / maxHeight / minHeight 里本仓库补充的档位 */
function readSizeKeys(configSource, group) {
  const start = configSource.indexOf(`${group}: {`);
  if (start < 0) {
    return new Set();
  }
  const end = configSource.indexOf("}", start);
  const block = configSource.slice(start, end);
  return new Set([...block.matchAll(/^\s*"?([\w-]+)"?:\s*"/gm)].map(match => match[1]));
}

/** 提取 theme.colors 里每个色板的色阶集合 */
function readPalettes(configSource) {
  const start = configSource.indexOf("colors: {");
  if (start < 0) {
    throw new Error("uno.config.ts 里找不到 theme.colors");
  }

  // 找到 colors: { ... } 的配对右括号
  let depth = 0;
  let end = start;
  for (let i = start; i < configSource.length; i += 1) {
    if (configSource[i] === "{") {
      depth += 1;
    }
    else if (configSource[i] === "}") {
      depth -= 1;
      if (depth === 0) {
        end = i;
        break;
      }
    }
  }
  const block = configSource.slice(start, end);

  const palettes = new Map();
  const colorStart = /^\s{6}([a-z][\w-]*):\s*\{/gim;
  for (const match of block.matchAll(colorStart)) {
    const name = match[1];
    let depth2 = 0;
    const bodyStart = match.index + match[0].length - 1;
    let bodyEnd = bodyStart;
    for (let i = bodyStart; i < block.length; i += 1) {
      if (block[i] === "{") {
        depth2 += 1;
      }
      else if (block[i] === "}") {
        depth2 -= 1;
        if (depth2 === 0) {
          bodyEnd = i;
          break;
        }
      }
    }
    const body = block.slice(bodyStart + 1, bodyEnd);
    const shades = new Set();
    for (const shadeMatch of body.matchAll(/^\s*"?([\w-]+)"?:\s*"/gm)) {
      shades.add(shadeMatch[1]);
    }
    palettes.set(name, shades);
  }
  return palettes;
}

/** 提取 theme.animation 里声明过的动画名（counts 里列出的就是全部） */
function readAnimations(configSource) {
  const start = configSource.indexOf("animation: {");
  if (start < 0) {
    throw new Error("uno.config.ts 里找不到 theme.animation");
  }
  const countsStart = configSource.indexOf("counts: {", start);
  const countsEnd = configSource.indexOf("}", countsStart);
  const block = configSource.slice(countsStart, countsEnd);
  return new Set([...block.matchAll(/"([\w-]+)":/g)].map(match => match[1]));
}

function collectSourceFiles(directory, result = []) {
  for (const entry of readdirSync(directory)) {
    if (entry === "node_modules" || entry === "dist") {
      continue;
    }
    const path = join(directory, entry);
    if (statSync(path).isDirectory()) {
      collectSourceFiles(path, result);
    }
    else if (/\.(?:ts|tsx)$/.test(entry)) {
      result.push(path);
    }
  }
  return result;
}

/**
 * 读取已安装的图标集合：`@iconify-json/<name>/icons.json` 是纯 JSON，
 * 名字都在 `icons` 里（`aliases` 也要算，别名同样能生成 CSS）。
 */
function readIconSets() {
  const directory = join(frontendRoot, "node_modules", "@iconify-json");
  const sets = new Map();
  let entries = [];
  try {
    entries = readdirSync(directory);
  }
  catch {
    return sets;
  }
  for (const entry of entries) {
    try {
      const data = JSON.parse(
        readFileSync(join(directory, entry, "icons.json"), "utf8"),
      );
      sets.set(
        entry,
        new Set([
          ...Object.keys(data.icons ?? {}),
          ...Object.keys(data.aliases ?? {}),
        ]),
      );
    }
    catch {
      // 不是图标集合目录（或没有 icons.json）：跳过，不影响其它检查
    }
  }
  return sets;
}

const palettes = readPalettes(readFileSync(configPath, "utf8"));
const animations = readAnimations(readFileSync(configPath, "utf8"));
const paletteNames = [...palettes.keys()].join("|");

// 尺寸类名：presetWind3 的主题 + 本仓库 theme 的补充 + 关键字
const configSource = readFileSync(configPath, "utf8");
const frameworkTheme = presetWind3({ dark: "class" }).theme ?? {};
const sizeScales = {
  "max-w": [
    ...Object.keys(frameworkTheme.width ?? {}),
    ...Object.keys(frameworkTheme.maxWidth ?? {}),
    ...readSizeKeys(configSource, "maxWidth"),
  ],
  "min-w": [
    ...Object.keys(frameworkTheme.width ?? {}),
    ...Object.keys(frameworkTheme.minWidth ?? {}),
    ...readSizeKeys(configSource, "minWidth"),
  ],
  "max-h": [
    ...Object.keys(frameworkTheme.height ?? {}),
    ...Object.keys(frameworkTheme.maxHeight ?? {}),
    ...readSizeKeys(configSource, "maxHeight"),
  ],
  "min-h": [
    ...Object.keys(frameworkTheme.height ?? {}),
    ...Object.keys(frameworkTheme.minHeight ?? {}),
    ...readSizeKeys(configSource, "minHeight"),
  ],
};
const validSizeNames = new Map(
  Object.entries(sizeScales).map(([prefix, names]) => [
    prefix,
    new Set([...names, ...SIZE_KEYWORDS]),
  ]),
);

// uno.config.ts 自己也要查：shortcuts（yh-glass / yh-chip / glass-*）里的类名
// 同样是字符串，写错一个色阶整条 shortcut 会静默失效。
const filesToScan = [...collectSourceFiles(sourceDirectory), configPath];

const colorPattern = new RegExp(
  String.raw`\b(?:bg|text|border|ring|ring-offset|from|via|to|fill|stroke|divide|outline|placeholder|decoration|shadow|caret|accent)-(?:${paletteNames})-(?:[0-9]{2,3})\b`,
  "g",
);
const colorNameCapture = new RegExp(
  String.raw`\b(?:bg|text|border|ring|ring-offset|from|via|to|fill|stroke|divide|outline|placeholder|decoration|shadow|caret|accent)-(${paletteNames})-([0-9]{2,3})\b`,
  "g",
);
const animatePattern = /\banimate-([a-z][\w-]*)\b/g;
// 名字可以以数字开头（2xl / 8xl），但纯数字不查（那是 spacing 档位，一定存在）
const sizePattern = /\b(max-w|min-w|max-h|min-h)-([a-z][\w-]*|\d+[a-z][\w-]*)\b/g;

// 图标：只有集合名来自**已安装的** @iconify-json 时才参与判断，
// 这样 `i-mdi-x` / `i-some-other-x` 之类的普通字符串不会被误报。
const iconSets = readIconSets();
const iconPattern = iconSets.size > 0
  ? new RegExp(
      String.raw`\bi-(${[...iconSets.keys()].join("|")})-([a-z0-9]+(?:-[a-z0-9]+)*)\b`,
      "g",
    )
  : null;

const problems = [];

for (const path of filesToScan) {
  const lines = readFileSync(path, "utf8").split("\n");
  const displayPath = relative(frontendRoot, path).replace(/\\/g, "/");

  lines.forEach((line, index) => {
    // 跳过注释行，注释里的类名不参与生成
    const trimmed = line.trim();
    if (trimmed.startsWith("//") || trimmed.startsWith("*") || trimmed.startsWith("/*")) {
      return;
    }

    for (const match of line.matchAll(colorPattern)) {
      const full = match[0];
      colorNameCapture.lastIndex = 0;
      const parsed = colorNameCapture.exec(full);
      if (!parsed) {
        continue;
      }
      const [, color, shade] = parsed;
      const shades = palettes.get(color);
      if (shades && !shades.has(shade)) {
        problems.push(
          `${displayPath}:${index + 1}  色板 ${color} 没有 ${shade} 这一档 → ${full} 会被静默丢弃`,
        );
      }
    }

    for (const match of line.matchAll(animatePattern)) {
      const name = match[1];
      if (BUILTIN_ANIMATIONS.has(name) || animations.has(name)) {
        continue;
      }
      problems.push(
        `${displayPath}:${index + 1}  主题里没有动画 ${name} → ${match[0]} 会被静默丢弃`,
      );
    }

    for (const match of line.matchAll(sizePattern)) {
      const [full, prefix, name] = match;
      if (validSizeNames.get(prefix)?.has(name)) {
        continue;
      }
      problems.push(
        `${displayPath}:${index + 1}  主题里没有尺寸档位 ${name} → ${full} 会被静默丢弃`,
      );
    }

    if (iconPattern) {
      for (const match of line.matchAll(iconPattern)) {
        const [full, collection, name] = match;
        if (iconSets.get(collection)?.has(name)) {
          continue;
        }
        problems.push(
          `${displayPath}:${index + 1}  @iconify-json/${collection} 里没有图标 ${name} → ${full} 会渲染成空白`,
        );
      }
    }
  });
}

if (problems.length > 0) {
  console.error("发现会被 UnoCSS 静默丢弃的工具类：\n");
  for (const problem of problems) {
    console.error(`  ${problem}`);
  }
  console.error(
    `\n共 ${problems.length} 处。请在 uno.config.ts 的 theme 里补齐色阶 / 动画 / 尺寸档位，`
    + `或改用 @iconify-json 里真实存在的图标名后再提交。`,
  );
  process.exit(1);
}

console.log(
  "UnoCSS 令牌检查通过：色阶、动画、尺寸档位、图标都没有引用未定义的令牌。",
);
