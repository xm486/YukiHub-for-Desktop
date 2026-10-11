/**
 * 游戏可执行文件路径的「展示值 / 落库值」换算。
 *
 * 「编辑」页与「启动配置」页编辑的是**同一个字段** `game.path`：
 *
 * - 展示：可执行文件就在游戏目录下时只显示相对文件名（短、好读），
 *   否则显示完整路径；
 * - 落库：用户手输了相对文件名时，按游戏目录补回绝对路径。
 *
 * 两处必须共用这一份实现 —— 各写一份的话，同一份数据会出现两种显示、
 * 两种落库结果（用户看到的「可执行程序」和「可执行文件」就会对不上）。
 */

export function getExecutableDisplayPath(
  executablePath: string,
  gameDirectory?: string,
): string {
  const normalizedDirectory = (gameDirectory || "")
    .replaceAll("/", "\\")
    .replace(/\\+$/, "");
  const normalizedExecutablePath = executablePath.replaceAll("/", "\\");
  const directoryPrefix = `${normalizedDirectory}\\`;

  if (
    normalizedDirectory
    && normalizedExecutablePath
      .toLowerCase()
      .startsWith(directoryPrefix.toLowerCase())
  ) {
    return executablePath.slice(directoryPrefix.length);
  }

  return executablePath;
}

export function resolveExecutablePath(
  executablePath: string,
  gameDirectory?: string,
): string {
  if (
    !executablePath
    || !gameDirectory
    || /^(?:[a-z]:[\\/]|[\\/]{2})/i.test(executablePath)
  ) {
    return executablePath;
  }

  const directory = gameDirectory.replace(/[\\/]+$/, "");
  const separator = directory.includes("\\") ? "\\" : "/";
  return `${directory}${separator}${executablePath.replace(/^[\\/]+/, "")}`;
}
