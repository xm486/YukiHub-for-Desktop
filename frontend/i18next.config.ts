import { defineConfig } from "i18next-cli";

export default defineConfig({
  locales: ["en-US", "zh-CN", "zh-TW", "ja-JP"],
  extract: {
    input: "src/**/*.{js,jsx,ts,tsx}",
    output: "src/locales/{{language}}.json",
    defaultNS: false,
    removeUnusedKeys: true,
    disablePlurals: true,
    sort: false,
    preservePatterns: [
      "bigScreen.categoryAll",
      "bigScreen.categoryRecent",
      "common.allStatus",
      "common.name",
      "common.company",
      "common.lastPlayedAt",
      "common.rating",
      "common.releaseDate",
      "gameProgress.spoilerBoundaryOpts.*",
      "gameLaunch.steamLaunchOptionsPresets.*",
      "friendsChat.section.*",
      "friendsChat.status.*",
      "gameStats.periodStatsLabel.*",
      // 这两个失败提示把键名当字符串传给 run()，提取器看不到（成功提示是字面量）
      "settings.portableSetup.toast.protocolRegisterFailed",
      "settings.portableSetup.toast.protocolUnregisterFailed",
      "metadataUpdateFields.*",
      "settings.appearance.gameCardLayout_*",
      // 快捷键的文案键由 GLOBAL_SHORTCUTS 的 id 拼出来，提取器看不到
      "shortcuts.*",
    ],
  },
});
