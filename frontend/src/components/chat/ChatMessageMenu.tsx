import type { ChatMessage } from "../../../bindings/yukihub/internal/service/yukihubaccount/models";

import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

interface ChatMessageMenuProps {
  isOpen: boolean;
  message: ChatMessage | null;
  /** 菜单出现的屏幕坐标（鼠标位置 / 长按位置） */
  position: { x: number; y: number };
  /** 私聊 or 群聊：决定是否出现「撤回/删除（管理）」 */
  isGroup: boolean;
  /** 当前用户在该群是否管理员（群聊才有意义） */
  isGroupAdmin: boolean;
  onClose: () => void;
  onCopy: () => void;
  onReply: () => void;
  onReport: (reason: string) => void;
  onRecall: () => void;
  onDelete: () => void;
  onViewProfile: () => void;
}

/**
 * 聊天消息操作菜单（对齐手机版 showChatMsgOptions / showGroupMsgOptions）。
 *
 * 菜单项与手机版严格一致：
 *   - 复制：仅文本消息
 *   - 回复：所有消息
 *   - 举报：不是自己的消息 且 已落库（id 非空）
 *   - 撤回 / 删除（管理）：聊天室管理员 且 已落库
 *   - 查看资料：能确定对方 UID 时
 *
 * 用 fixed 定位直接贴在鼠标/手指位置，不做边界翻转（PC 上菜单很小，
 * 贴边场景很少），点击外部任意处关闭。
 */
export function ChatMessageMenu({
  isOpen,
  message,
  position,
  isGroup,
  isGroupAdmin,
  onClose,
  onCopy,
  onReply,
  onReport,
  onRecall,
  onDelete,
  onViewProfile,
}: ChatMessageMenuProps) {
  const { t } = useTranslation();
  const menuRef = useRef<HTMLDivElement | null>(null);
  // 举报是二级菜单：先选理由再提交（手机版 showReportReasonDialog 同流程）
  const [reportMode, setReportMode] = useState(false);
  // 自定义举报理由（手机版同款输入框，上限 50 字符）
  const [customReason, setCustomReason] = useState("");

  useEffect(() => {
    if (!isOpen) {
      setReportMode(false);
    }
  }, [isOpen]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (menuRef.current?.contains(event.target as Node)) {
        return;
      }
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("mousedown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [isOpen, onClose]);

  if (!isOpen || !message) {
    return null;
  }

  const isText = !message.msgType || message.msgType === "text";
  const isMine = Boolean(message.isMine);
  const hasId = Boolean(message.id);
  const canViewProfile = Boolean(message.senderUid) && !isMine;

  const items: Array<{
    key: string;
    label: string;
    danger?: boolean;
    onClick: () => void;
  }> = [];

  if (isText) {
    items.push({
      key: "copy",
      label: t("friendsChat.menuCopy"),
      onClick: onCopy,
    });
  }
  items.push({
    key: "reply",
    label: t("friendsChat.menuReply"),
    onClick: onReply,
  });
  if (canViewProfile) {
    items.push({
      key: "profile",
      label: t("friendsChat.menuProfile"),
      onClick: onViewProfile,
    });
  }
  // 手机版：举报仅对「他人 + 已落库」的消息开放
  if (!isMine && hasId) {
    items.push({
      key: "report",
      label: t("friendsChat.menuReport"),
      danger: true,
      onClick: () => {
        // 每次进举报二级菜单都从空输入框开始：菜单是跨消息复用的，
        // 不清空会把上一条消息填过的自定义理由带过来。
        setCustomReason("");
        setReportMode(true);
      },
    });
  }
  // 手机版：撤回/删除只给聊天室管理员，且消息必须已落库
  if (isGroup && isGroupAdmin && hasId) {
    items.push({
      key: "recall",
      label: t("friendsChat.menuRecall"),
      onClick: onRecall,
    });
    items.push({
      key: "delete",
      label: t("friendsChat.menuDelete"),
      danger: true,
      onClick: onDelete,
    });
  }

  return (
    <div
      ref={menuRef}
      role="menu"
      className="fixed z-[80] min-w-[148px] overflow-hidden rounded-xl border border-brand-200/90 bg-white py-1 shadow-xl dark:border-brand-700/90 dark:bg-brand-800"
      style={{ left: position.x, top: position.y }}
    >
      {reportMode ? (
        <>
          <div className="px-3 py-1.5 text-[11px] font-semibold text-brand-500 dark:text-brand-400">
            {t("friendsChat.reportTitle")}
          </div>
          <p className="px-3 pb-1 text-[10px] leading-snug text-brand-400 dark:text-brand-500">
            {t("friendsChat.reportHint")}
          </p>
          {/* 五项预设理由，顺序与手机版 showReportReasonDialog 的 reasons[] 逐字一致 */}
          {[
            t("friendsChat.reportReasonSpam"),
            t("friendsChat.reportReasonPorn"),
            t("friendsChat.reportReasonAbuse"),
            t("friendsChat.reportReasonIllegal"),
            t("friendsChat.reportReasonFraud"),
          ].map(reason => (
            <button
              key={reason}
              type="button"
              role="menuitem"
              onClick={() => {
                onClose();
                onReport(reason);
              }}
              className="block w-full px-3 py-2 text-left text-sm text-brand-800 transition-colors hover:bg-brand-100 dark:text-brand-100 dark:hover:bg-brand-700/70"
            >
              {reason}
            </button>
          ))}
          {/*
            自定义理由：手机版在预设之外还给一个输入框。
            50 是**服务端 reason 字段的上限**，不是随手取的 UI 限制 ——
            前端不拦的话超长部分会被服务端静默截断，用户看到的和提交的不一致。
          */}
          <div className="mt-1 border-t border-brand-200/70 px-2 pt-1.5 dark:border-brand-700/70">
            <p className="px-1 text-[10px] text-brand-400 dark:text-brand-500">
              {t("friendsChat.reportCustomLabel")}
            </p>
            <input
              type="text"
              value={customReason}
              maxLength={50}
              onChange={event => setCustomReason(event.target.value)}
              placeholder={t("friendsChat.reportCustomPlaceholder")}
              className="mt-1 w-full rounded-lg border border-brand-200 bg-white px-2 py-1.5 text-xs text-brand-800 outline-none placeholder:text-brand-400 focus:border-primary-400 dark:border-brand-700 dark:bg-brand-900 dark:text-brand-100 dark:placeholder:text-brand-500"
            />
            <button
              type="button"
              role="menuitem"
              disabled={customReason.trim().length === 0}
              onClick={() => {
                const reason = customReason.trim();
                if (!reason) {
                  return;
                }
                onClose();
                onReport(reason);
              }}
              className="mt-1 block w-full rounded-lg bg-primary-500 px-3 py-1.5 text-xs font-semibold text-white transition-colors hover:bg-primary-600 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-primary-500"
            >
              {t("friendsChat.reportCustomSubmit")}
            </button>
          </div>
          <button
            type="button"
            role="menuitem"
            onClick={() => setReportMode(false)}
            className="mt-0.5 block w-full border-t border-brand-200/70 px-3 py-2 text-left text-sm text-brand-500 transition-colors hover:bg-brand-100 dark:border-brand-700/70 dark:text-brand-400 dark:hover:bg-brand-700/70"
          >
            {t("common.back")}
          </button>
        </>
      ) : (
        items.map(item => (
          <button
            key={item.key}
            type="button"
            role="menuitem"
            onClick={() => {
              onClose();
              item.onClick();
            }}
            className={`block w-full px-3 py-2 text-left text-sm transition-colors hover:bg-brand-100 dark:hover:bg-brand-700/70 ${
              item.danger
                ? "text-error-600 dark:text-error-400"
                : "text-brand-800 dark:text-brand-100"
            }`}
          >
            {item.label}
          </button>
        ))
      )}
    </div>
  );
}
