import { Browser } from "@wailsio/runtime";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";

import { GetVersionInfo } from "../../../bindings/yukihub/internal/service/versionservice";
import { BetterButton } from "../ui/better/BetterButton";

/**
 * 关于与开源许可面板。
 *
 * 除了展示版本信息，它还承担许可证义务：AGPL-3.0 要求交互式界面在分发时
 * 展示适当的法律声明（上游项目、许可证、修改声明）。
 * 版本与来源信息全部来自后端 GetVersionInfo，避免在前端再硬编码一份。
 */
export function AboutPanel() {
  const { t } = useTranslation();
  const [versionInfo, setVersionInfo] = useState<Record<
    string,
    string | undefined
  > | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const info = await GetVersionInfo();
        if (!cancelled) {
          setVersionInfo({ ...info });
        }
      }
      catch {
        if (!cancelled) {
          setVersionInfo(null);
        }
      }
    };
    void load();
    return () => {
      cancelled = true;
    };
  }, []);

  const appName = versionInfo?.appName ?? "";
  const repoURL = versionInfo?.repo ?? "";
  const upstreamURL = versionInfo?.upRepo ?? "";
  const upstreamName = versionInfo?.upstream ?? "";
  const upstreamVersion = versionInfo?.upVersion ?? "";

  return (
    <div className="space-y-5">
      <div className="space-y-1">
        {/* 产品名只从后端 version.AppDisplayName 取，语言文件里不再留第二份
            （改名时只改 internal/version 一处，四语言文案不会各自漂移）。 */}
        {appName !== "" && (
          <p className="text-sm font-medium text-brand-700 dark:text-brand-300">
            {appName}
          </p>
        )}
        <p className="text-xs text-brand-500 dark:text-brand-400">
          {t("settings.about.tagline")}
        </p>
      </div>

      {versionInfo && (
        <div className="space-y-2">
          <InfoRow
            label={t("settings.about.version")}
            value={versionInfo.version ?? "-"}
          />
          <InfoRow
            label={t("settings.about.commit")}
            value={versionInfo.commit ?? "-"}
          />
          <InfoRow
            label={t("settings.about.buildMode")}
            value={versionInfo.buildMode ?? "-"}
          />
          <InfoRow
            label={t("settings.about.buildTime")}
            value={versionInfo.buildTime ?? "-"}
          />
        </div>
      )}

      <div className="space-y-2">
        <p className="text-sm font-medium text-brand-700 dark:text-brand-300">
          {t("settings.about.licenseTitle")}
        </p>
        <p className="text-xs leading-relaxed text-brand-500 dark:text-brand-400">
          {t("settings.about.licenseBody")}
        </p>
      </div>

      <div className="space-y-2">
        <p className="text-sm font-medium text-brand-700 dark:text-brand-300">
          {t("settings.about.upstreamTitle")}
        </p>
        <p className="text-xs leading-relaxed text-brand-500 dark:text-brand-400">
          {t("settings.about.upstreamBody", {
            name: upstreamName || "-",
            version: upstreamVersion || "-",
          })}
        </p>
        <p className="text-xs leading-relaxed text-brand-500 dark:text-brand-400">
          {t("settings.about.upstreamNotice")}
        </p>
      </div>

      <div className="space-y-2">
        <p className="text-sm font-medium text-brand-700 dark:text-brand-300">
          {t("settings.about.thirdPartyTitle")}
        </p>
        <p className="text-xs leading-relaxed text-brand-500 dark:text-brand-400">
          {t("settings.about.thirdPartyBody")}
        </p>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        {repoURL !== "" && (
          <BetterButton
            type="button"
            variant="secondary"
            icon="i-mdi-github"
            onClick={() => void Browser.OpenURL(repoURL)}
          >
            {t("settings.about.openRepo")}
          </BetterButton>
        )}
        {upstreamURL !== "" && (
          <BetterButton
            type="button"
            variant="secondary"
            icon="i-mdi-open-in-new"
            onClick={() => void Browser.OpenURL(upstreamURL)}
          >
            {t("settings.about.openUpstream")}
          </BetterButton>
        )}
      </div>
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <span className="shrink-0 text-xs text-brand-500 dark:text-brand-400">
        {label}
      </span>
      <span className="text-right text-xs font-medium break-all text-brand-700 dark:text-brand-300">
        {value}
      </span>
    </div>
  );
}
