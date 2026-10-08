import type { appconf } from "../../src/bindings/models";
import { createRoute } from "@tanstack/react-router";
import { Browser } from "@wailsio/runtime";
import { useEffect, useRef, useState } from "react";

import { useTranslation } from "react-i18next";

import { GetVersionInfo } from "../../bindings/yukihub/internal/service/versionservice";
import { AboutPanel } from "../components/panel/AboutPanel";
import { AISettingsPanel } from "../components/panel/AISettingsPanel";
import { AppDataSettingsPanel } from "../components/panel/AppDataSettingsPanel";
import { BackgroundSettingsPanel } from "../components/panel/BackgroundSettingsPanel";
import { BackupSettingsPanel } from "../components/panel/BackupSettingsPanel";
import { BasicSettingsPanel } from "../components/panel/BasicSettingsPanel";
import { BigScreenSettingsPanel } from "../components/panel/BigScreenSettingsPanel";
import { FullDataBackupPanel } from "../components/panel/FullDataBackupPanel";
import { GameSettingsPanel } from "../components/panel/GameSettingsPanel";
import { MetadataSettingsPanel } from "../components/panel/MetadataSettingsPanel";
import { PortableSetupPanel } from "../components/panel/PortableSetupPanel";
import { SelfSyncPanel } from "../components/panel/SelfSyncPanel";
import { UpdateSettingsPanel } from "../components/panel/UpdateSettingsPanel";
import { SettingsSkeleton } from "../components/skeleton/SettingsSkeleton";
import { CollapsibleSection } from "../components/ui/CollapsibleSection";
import { SettingsSubSection } from "../components/ui/SettingsSubSection";
import { useAppStore } from "../store";
import { Route as rootRoute } from "./__root";

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/settings",
  component: SettingsPage,
});

function SettingsPage() {
  const { t } = useTranslation();
  const config = useAppStore(state => state.config);
  const draftConfig = useAppStore(state => state.draftConfig);
  const platformGOOS = useAppStore(state => state.platformGOOS);
  const backgroundProcessMuteSupported = useAppStore(
    state => state.backgroundProcessMuteSupported,
  );
  const fetchConfig = useAppStore(state => state.fetchConfig);
  const patchLiveConfig = useAppStore(state => state.patchLiveConfig);
  const applyGameLibraryPathChange = useAppStore(
    state => state.applyGameLibraryPathChange,
  );
  const resetDraftConfig = useAppStore(state => state.resetDraftConfig);
  const saveDraftConfig = useAppStore(state => state.saveDraftConfig);
  const setDraftConfig = useAppStore(state => state.setDraftConfig);
  const [isLoading, setIsLoading] = useState(true);
  const [showSkeleton, setShowSkeleton] = useState(false);
  const [versionInfo, setVersionInfo] = useState<Record<
    string,
    string | undefined
  > | null>(null);
  const isInitialMount = useRef(true);

  useEffect(() => {
    let cancelled = false;

    const loadConfig = async () => {
      setIsLoading(true);
      try {
        await fetchConfig();
      }
      catch (err) {
        console.error("Failed to fetch config:", err);
      }
      finally {
        if (!cancelled) {
          setIsLoading(false);
          isInitialMount.current = false;
        }
      }
    };

    const loadVersionInfo = async () => {
      try {
        const info = await GetVersionInfo();
        if (!cancelled) {
          setVersionInfo(info);
        }
      }
      catch (err) {
        console.error("Failed to fetch version info:", err);
      }
    };

    void loadConfig();
    void loadVersionInfo();

    return () => {
      cancelled = true;
    };
  }, [fetchConfig]);

  useEffect(() => {
    let timer: number;
    if (isLoading) {
      timer = window.setTimeout(() => {
        setShowSkeleton(true);
      }, 300);
    }
    else {
      setShowSkeleton(false);
    }
    return () => clearTimeout(timer);
  }, [isLoading]);

  useEffect(() => {
    if (!config || isInitialMount.current) {
      return;
    }

    resetDraftConfig();
  }, [config, resetDraftConfig]);

  useEffect(() => {
    if (!draftConfig || !config || isInitialMount.current) {
      return;
    }

    const hasChanges = JSON.stringify(draftConfig) !== JSON.stringify(config);
    if (!hasChanges) {
      return;
    }

    const timer = setTimeout(() => {
      void saveDraftConfig();
    }, 250);

    return () => clearTimeout(timer);
  }, [config, draftConfig, saveDraftConfig]);

  const handleDraftChange = (newData: appconf.AppConfig) => {
    setDraftConfig(newData);
  };

  const handleZoomChange = (zoomFactor: number) => {
    void patchLiveConfig({ window_zoom_factor: zoomFactor });
  };

  if (isLoading && (!config || !draftConfig)) {
    if (!showSkeleton) {
      return null;
    }
    return <SettingsSkeleton />;
  }

  if (!config || !draftConfig) {
    return (
      <div className="flex min-h-[80vh] flex-col items-center justify-center space-y-4 text-brand-500">
        <div className="i-mdi-cog-outline animate-spin-slow text-6xl" />
        <p className="text-xl">{t("settings.preparingSettings")}</p>
      </div>
    );
  }

  return (
    <div
      className={`mx-auto max-w-8xl space-y-6 p-8 transition-opacity duration-300 ${isLoading ? "pointer-events-none opacity-50" : "opacity-100"}`}
    >
      <div className="yh-glass flex items-center gap-3 px-5 py-4">
        <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-gradient-to-b from-primary-400 to-primary-600 text-white shadow-md">
          <span className="i-mdi-cog-outline text-xl" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-2xl font-bold text-brand-900 dark:text-white">
            {t("settings.title")}
          </h1>
          <p className="truncate text-sm text-brand-500 dark:text-brand-400">
            {t("settings.subtitle")}
          </p>
        </div>
      </div>

      <div className="mx-auto w-full max-w-5xl space-y-6">
        <CollapsibleSection
          title={t("settings.sections.basic")}
          icon="i-mdi-database-settings"
          defaultOpen={true}
        >
          <BasicSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
            onZoomChange={handleZoomChange}
            onConfigRefresh={fetchConfig}
            onGameLibraryPathApply={applyGameLibraryPathChange}
          />
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.metadata")}
          icon="i-mdi-database-search"
          defaultOpen={false}
        >
          <MetadataSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
          />
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.appearance")}
          icon="i-mdi-palette"
          defaultOpen={false}
        >
          <BackgroundSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
          />
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.bigScreen")}
          icon="i-mdi-television-classic"
          defaultOpen={false}
        >
          <BigScreenSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
          />
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.game")}
          icon="i-mdi-timer-play-outline"
          defaultOpen={false}
        >
          <GameSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
            goos={platformGOOS}
            backgroundProcessMuteSupported={backgroundProcessMuteSupported}
          />
        </CollapsibleSection>

        {/*
          所有「我的数据」相关的东西都收在这一个分区里：云端、自动备份、
          数据库备份、导入导出、数据目录与日志。
          原本它们分成四个顶层分区（云配置 / 同步与备份 / 数据库备份 /
          全量数据备份 / 应用数据），用户根本分不清该点哪个。
        */}
        <CollapsibleSection
          title={t("settings.sections.dataManagement")}
          icon="i-mdi-database-cog-outline"
          defaultOpen={false}
        >
          <SettingsSubSection
            title={t("settings.subSections.backup")}
            hint={t("settings.subSections.backupHint")}
            icon="i-mdi-backup-restore"
          >
            <BackupSettingsPanel
              formData={draftConfig}
              onChange={handleDraftChange}
            />
          </SettingsSubSection>

          <SettingsSubSection
            title={t("settings.subSections.selfSync")}
            hint={t("settings.subSections.selfSyncHint")}
            icon="i-mdi-cloud-sync-outline"
          >
            <SelfSyncPanel />
          </SettingsSubSection>

          <SettingsSubSection
            title={t("settings.sections.migration")}
            icon="i-mdi-package-variant"
          >
            <FullDataBackupPanel />
          </SettingsSubSection>

          <SettingsSubSection
            title={t("settings.sections.appData")}
            hint={t("settings.subSections.appDataHint")}
            icon="i-mdi-folder-cog-outline"
          >
            <AppDataSettingsPanel />
            {["portable", "appimage"].includes(
              versionInfo?.buildMode ?? "",
            ) && <PortableSetupPanel />}
          </SettingsSubSection>
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.ai")}
          icon="i-mdi-robot-happy"
          defaultOpen={false}
        >
          <AISettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
          />
        </CollapsibleSection>

        <CollapsibleSection
          title={t("settings.sections.aboutUpdate")}
          icon="i-mdi-information-outline"
          defaultOpen={false}
        >
          <UpdateSettingsPanel
            formData={draftConfig}
            onChange={handleDraftChange}
          />
          <SettingsSubSection
            title={t("settings.subSections.about")}
            icon="i-mdi-license"
          >
            <AboutPanel />
          </SettingsSubSection>
        </CollapsibleSection>
      </div>

      <div className="pt-4 text-center text-brand-500 dark:text-brand-400 pb-8 flex flex-col items-center justify-center">
        {/* 产品名取自后端 GetVersionInfo.appName（internal/version 是唯一来源）；
            兜底用产品家族名 YukiHub，避免在前端再写一份显示名。 */}
        <p className="text-xs">
          {versionInfo?.appName ?? "YukiHub"}
          {" "}
          — a modified fork of LunaBox by
          Saramanda9988 &amp; contributors.
        </p>
        {versionInfo && (
          <p className="mt-1 text-xs opacity-80">
            Version
            {" "}
            {versionInfo.version}
            {" "}
            (
            {versionInfo.commit}
            ) |
            {" "}
            {versionInfo.buildMode}
            {" "}
            | Built at
            {versionInfo.buildTime}
          </p>
        )}

        <button
          type="button"
          onClick={() =>
            void Browser.OpenURL("https://github.com/xm486/YukiHub")}
          className="mt-6 inline-flex items-center justify-center gap-2 rounded-full border border-primary-200/70 bg-white/70 px-4 py-2 text-sm font-semibold text-brand-700 transition-colors hover:bg-white dark:border-primary-300/40 dark:bg-[#1D2B3E]/70 dark:text-white/90 dark:hover:bg-[#1D2B3E]"
        >
          <div className="i-mdi-github text-xl" />
          <span>{t("settings.github")}</span>
        </button>
      </div>
    </div>
  );
}
