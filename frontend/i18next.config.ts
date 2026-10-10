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
      // 平台名由 presencePlatformLabelKey() 拼成 friendsChat.platform.<平台>，提取器看不到。
      // 这里刻意用**具体键名**而不是 friendsChat.platform.*：被 preserve 的集合越小越好，
      // 以后这个前缀下再新增键时才会照常参与 i18n:clean 的孤儿检测。
      "friendsChat.platform.android",
      "friendsChat.platform.pc",
      "friendsChat.platform.web",
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
