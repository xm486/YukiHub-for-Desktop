import type {
  ChatEmoji,
  ChatGroup,
  ChatMessage,
  Friend,
  FriendList,
  FriendRequest,
  FriendRequests,
} from "../../../bindings/yukihub/internal/service/yukihubaccount/models";
import type { vo } from "../../../src/bindings/models";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import toast from "react-hot-toast";
import { useTranslation } from "react-i18next";
import {
  AcceptFriendRequest,
  GetChatHistory,
  GetGroupHistory,
  GetGroupOnlineCount,
  ListChatEmojis,
  ListChatGroups,
  ListChatStickerPacks,
  ListChatStickerURLs,
  ListFriendRequests,
  ListFriends,
  ManageGroupMessage,
  PollChatMessages,
  PollGroupMessages,
  RejectFriendRequest,
  RemoveFriend,
  ReportChatMessage,
  SearchUsers,
  SelectChatImage,
  SendChatMessage,
  SendFriendRequest,
  SendGroupMessage,
  SetFriendNote,
  UploadChatImage,
} from "../../../bindings/yukihub/internal/service/accountservice";
import { onWailsEvent } from "../../../src/bindings/runtime";
import { useAccountStatus } from "../../hooks/useAccountStatus";
import { resolveChatMediaURL } from "../../utils/chatMedia";
import { proxiedImageSrc } from "../../utils/imageProxy";
import {
  presencePlatformLabelKey,
  showsPresencePlatform,
} from "../../utils/presencePlatform";
import { ChatAvatar } from "../chat/ChatAvatar";
import {
  ChatMessageMedia,
  emojiDisplayURL as sharedEmojiDisplayURL,
} from "../chat/ChatMessageMedia";
import { ChatMessageMenu } from "../chat/ChatMessageMenu";
import { ImageViewerModal } from "../chat/ImageViewerModal";
import { levelBadgeStyle } from "../chat/levelBadge";
import { UserProfileModal } from "../chat/UserProfileModal";
import { BetterButton } from "../ui/better/BetterButton";
import { BetterInput } from "../ui/better/BetterInput";
import { ContextMenu } from "../ui/ContextMenu";
import { ModalPortal } from "../ui/ModalPortal";
import { PresencePlatformIcon } from "../ui/PresencePlatformIcon";
import { ConfirmModal } from "./ConfirmModal";

interface FriendsChatModalProps {
  isOpen: boolean;
  isLoggedIn: boolean;
  onClose: () => void;
}

/** 轮询间隔，与手机版 FriendsChatDialog.POLL_INTERVAL_MS 一致（10 秒） */
const POLL_INTERVAL_MS = 10_000;
/**
 * 好友列表推送事件（后端 account_friend_play.go 的 friendListUpdatedEvent）。
 * 后端有变化才推，且推的是整份列表，前端直接吃。
 */
const FRIEND_LIST_UPDATED_EVENT = "friend:list-updated";
/** 历史消息每页条数，与手机版一致 */
const HISTORY_PAGE_SIZE = 20;

type ChatTarget
  = | { kind: "friend"; friend: Friend }
    | { kind: "group"; group: ChatGroup };

type MainView = "list" | "requests" | "add" | "chat";

/** 好友备注长度上限，与手机版 FriendsChatDialog 的「最多50字」一致。 */
const FRIEND_NOTE_MAX_LENGTH = 50;

interface ChatDraft {
  text: string;
}

/** 表情/贴纸选择面板：0=本站表情 1=未萌贴纸包列表 2=包内表情（与手机版一致） */
type EmojiTab = 0 | 1 | 2;

/**
 * 消息排序比较器。
 *
 * **绝不能用 id 的字符串序**：服务端下发的 id 是纯数字字符串，字典序会退化成
 * 「按字符比」，例如 "9" > "1000"、"114514" < "9999"，整个列表顺序随机错乱
 * （用户看到的现象是「发完消息跳到上面/历史跑到下面」）。
 *
 * 优先级：createdAt（ISO 风格字符串，字典序即时间序）→ 数值 id → 保持原序。
 * 返回 0 时 Array.prototype.sort 是稳定排序，不会打乱同键消息的相对顺序。
 */
function compareMessages(a: ChatMessage, b: ChatMessage): number {
  const timeA = a.createdAt ?? "";
  const timeB = b.createdAt ?? "";
  if (timeA && timeB && timeA !== timeB) {
    return timeA < timeB ? -1 : 1;
  }
  const idA = Number(a.id);
  const idB = Number(b.id);
  if (Number.isFinite(idA) && Number.isFinite(idB) && idA !== idB) {
    return idA - idB;
  }
  return 0;
}

interface StickerPackView {
  id: string;
  title: string;
  cover: string;
  stickerCount: number;
}

/**
 * 好友 / 聊天弹窗，功能对齐手机版 FriendsChatDialog：
 *
 * - 好友列表按「正在游戏 → 在线 → 离线」分组（Steam 风格）
 * - 群组列表置顶
 * - 私聊 / 群聊：历史消息分页、10 秒轮询新消息、回复
 * - 添加好友：按 UID 或昵称搜索
 * - 好友请求：接受 / 拒绝
 */
export function FriendsChatModal({
  isOpen,
  isLoggedIn,
  onClose,
}: FriendsChatModalProps) {
  const { t } = useTranslation();
  // 自己的头像/昵称（群聊里自己的消息也要带头像，与手机版 QQ 式右侧布局一致）
  const accountStatus = useAccountStatus();

  const [view, setView] = useState<MainView>("list");
  const [friendList, setFriendList] = useState<FriendList | null>(null);
  const [requests, setRequests] = useState<FriendRequests | null>(null);
  const [requestsLoading, setRequestsLoading] = useState(false);
  const [groups, setGroups] = useState<ChatGroup[]>([]);
  const [groupOnlineCount, setGroupOnlineCount] = useState(0);
  const [isLoading, setIsLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [target, setTarget] = useState<ChatTarget | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [hasMoreHistory, setHasMoreHistory] = useState(false);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [draft, setDraft] = useState<ChatDraft>({ text: "" });
  const [isSending, setIsSending] = useState(false);
  const [replyTo, setReplyTo] = useState<ChatMessage | null>(null);
  const [searchKeyword, setSearchKeyword] = useState("");
  const [searchResults, setSearchResults] = useState<Friend[] | null>(null);
  const [isSearching, setIsSearching] = useState(false);

  // 表情 / 贴纸面板状态
  const [emojiPanelOpen, setEmojiPanelOpen] = useState(false);
  const [emojiTab, setEmojiTab] = useState<EmojiTab>(0);
  const [emojis, setEmojis] = useState<ChatEmoji[] | null>(null);
  const [stickerEnabled, setStickerEnabled] = useState(false);
  const [stickerPacks, setStickerPacks] = useState<StickerPackView[]>([]);
  const [activeStickerPack, setActiveStickerPack]
    = useState<StickerPackView | null>(null);
  const [stickerURLs, setStickerURLs] = useState<string[]>([]);
  const [stickerLoading, setStickerLoading] = useState(false);
  const [isUploadingImage, setIsUploadingImage] = useState(false);

  // 消息操作菜单（右键气泡 / 长按）
  const [menuMessage, setMenuMessage] = useState<ChatMessage | null>(null);
  const [menuPosition, setMenuPosition] = useState({ x: 0, y: 0 });
  // 用户资料弹窗
  const [profileUid, setProfileUid] = useState(0);
  // 好友列表项的右键菜单（点整行仍然是进私聊，右键才出「查看资料/发消息」）
  const [friendMenu, setFriendMenu] = useState<Friend | null>(null);
  const [friendMenuPosition, setFriendMenuPosition] = useState({ x: 0, y: 0 });
  // 「设置备注」对话框的目标好友与草稿（上限 50 字，与手机版一致）
  const [noteTarget, setNoteTarget] = useState<Friend | null>(null);
  const [noteDraft, setNoteDraft] = useState("");
  const [savingNote, setSavingNote] = useState(false);
  // 「删除好友」确认框的目标好友
  const [deleteTarget, setDeleteTarget] = useState<Friend | null>(null);
  const [deletingFriend, setDeletingFriend] = useState(false);
  // 图片全屏查看
  const [viewerImageURL, setViewerImageURL] = useState("");

  const listRef = useRef<HTMLDivElement | null>(null);
  const oldestIdRef = useRef<string>("");
  const targetRef = useRef<ChatTarget | null>(null);
  const afterIdRef = useRef<string>("");
  const pollTimerRef = useRef<number | null>(null);
  // 输入区容器：插入 @提及 后把焦点还给里面的 input
  const inputWrapRef = useRef<HTMLDivElement | null>(null);

  targetRef.current = target;

  // 聊天图片 / 头像框素材的地址补全走公共工具（服务端下发的是相对路径）
  const resolveChatImageURL = resolveChatMediaURL;

  /** 表情名 → 可显示 URL（映射规则与浮层共用一份，见 ChatMessageMedia） */
  const emojiDisplayURL = (content: string): string =>
    sharedEmojiDisplayURL(content, emojis);

  /** 懒加载表情与贴纸数据（首次打开面板时） */
  const loadEmojiPanelData = async () => {
    if (emojis === null) {
      ListChatEmojis()
        .then(list => setEmojis(list))
        .catch(() => setEmojis([]));
    }
    if (!stickerEnabled && stickerPacks.length === 0) {
      ListChatStickerPacks()
        .then((list) => {
          setStickerEnabled(list.enabled);
          setStickerPacks(
            list.packs.map(pack => ({
              id: pack.id ?? "",
              title: pack.title ?? "",
              cover: pack.cover ?? "",
              stickerCount: pack.sticker_count ?? 0,
            })),
          );
        })
        .catch(() => setStickerEnabled(false));
    }
  };

  /** 进入某个贴纸包 */
  const openStickerPack = async (pack: StickerPackView) => {
    setStickerLoading(true);
    try {
      const urls = await ListChatStickerURLs(pack.id);
      if (urls.length === 0) {
        toast.error(t("friendsChat.stickerPackEmpty"));
        return;
      }
      setActiveStickerPack(pack);
      setStickerURLs(urls);
      setEmojiTab(2);
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setStickerLoading(false);
    }
  };

  /** 滚到消息底部 */
  const scrollToBottom = (smooth = true) => {
    requestAnimationFrame(() => {
      const el = listRef.current;
      if (el) {
        el.scrollTo({
          top: el.scrollHeight,
          behavior: smooth ? "smooth" : "auto",
        });
      }
    });
  };

  /**
   * 消息变化后校正滚动位置。
   *
   * 首次进入会话时 `scrollToBottom` 只跑一帧，此刻图片/内容还没撑开高度，
   * 滚到的其实是「假的底部」，用户随后会看到列表停在半中间。这里以「最后一条
   * 消息 id 变化」为触发点重滚，并在 180ms 后再校正一次。
   * 往上翻历史（前插）不会改变最后一条，因此不会打断阅读。
   */
  const lastMessageIdRef = useRef("");
  useEffect(() => {
    const last = messages[messages.length - 1];
    if (!last || view !== "chat") {
      return;
    }
    if (last.id === lastMessageIdRef.current) {
      return;
    }
    lastMessageIdRef.current = last.id;
    const el = listRef.current;
    const nearBottom
      = !el || el.scrollHeight - el.scrollTop - el.clientHeight < 160;
    if (!nearBottom && !last.isMine) {
      return;
    }
    scrollToBottom(false);
    const timer = window.setTimeout(() => scrollToBottom(false), 180);
    return () => window.clearTimeout(timer);
  }, [messages, view]);

  const mergeMessages = (incoming: ChatMessage[]) => {
    if (incoming.length === 0) {
      return;
    }
    setMessages((current) => {
      const seen = new Set(current.map(item => item.id));
      const merged = [...current];
      for (const item of incoming) {
        if (!seen.has(item.id)) {
          merged.push(item);
          seen.add(item.id);
        }
      }
      // 必须用 compareMessages：不能按 id 做字符串比较（消息 id 是纯数字字符串，
      // "9" > "1000" 会让顺序整体错乱，表现为「发完消息列表跳到上面去」）。
      merged.sort(compareMessages);
      return merged;
    });
  };

  /** 发送表情消息（本站表情传名字，贴纸传 URL，与手机版一致） */
  const sendEmoji = async (content: string) => {
    const current = target;
    if (!current || isSending) {
      return;
    }
    setIsSending(true);
    try {
      const isFriend = current.kind === "friend";
      const sent = isFriend
        ? await SendChatMessage(current.friend.id, content, "emoji", "")
        : await SendGroupMessage(current.group.id, content, "emoji", "");
      if (sent.id) {
        mergeMessages([sent]);
        afterIdRef.current = sent.id || afterIdRef.current;
      }
      setEmojiPanelOpen(false);
      scrollToBottom();
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsSending(false);
    }
  };

  /** 选图 → 上传 → 发图片消息（选图/上传在后端，前端只串流程） */
  const pickAndSendImage = async () => {
    if (isUploadingImage) {
      return;
    }
    setIsUploadingImage(true);
    try {
      const picked: vo.ChatImagePick = await SelectChatImage();
      if (!picked.path) {
        return; // 用户取消
      }
      const url = await UploadChatImage(picked.data, picked.mime_type);
      const current = target;
      if (!current) {
        return;
      }
      const isFriend = current.kind === "friend";
      const sent = isFriend
        ? await SendChatMessage(current.friend.id, url, "image", "")
        : await SendGroupMessage(current.group.id, url, "image", "");
      if (sent.id) {
        mergeMessages([sent]);
        afterIdRef.current = sent.id || afterIdRef.current;
      }
      scrollToBottom();
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsUploadingImage(false);
    }
  };

  /** 拉好友 + 群列表 */
  const refreshLists = async (withLoading: boolean) => {
    if (withLoading) {
      setIsLoading(true);
    }
    setLoadError(null);
    try {
      const [friends, groupItems] = await Promise.all([
        ListFriends(),
        ListChatGroups(),
      ]);
      setFriendList(friends);
      setGroups(groupItems);
    }
    catch (error) {
      setLoadError(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsLoading(false);
    }
  };

  // 打开时拉一次列表；关闭时清状态
  useEffect(() => {
    if (!isOpen) {
      setView("list");
      setTarget(null);
      setMessages([]);
      setReplyTo(null);
      setDraft({ text: "" });
      setSearchResults(null);
      setSearchKeyword("");
      return;
    }
    if (!isLoggedIn) {
      return;
    }
    void refreshLists(true);
  }, [isOpen, isLoggedIn]);

  const openNoteEditor = (friend: Friend) => {
    setNoteTarget(friend);
    setNoteDraft(friend.note ?? "");
  };

  const handleSaveNote = async () => {
    if (!noteTarget || savingNote) {
      return;
    }
    const note = noteDraft.trim();
    if (note.length > FRIEND_NOTE_MAX_LENGTH) {
      toast.error(
        t("friendsChat.noteTooLong", { max: FRIEND_NOTE_MAX_LENGTH }),
      );
      return;
    }
    setSavingNote(true);
    try {
      await SetFriendNote(noteTarget.id, note);
      setNoteTarget(null);
      await refreshLists(false);
      toast.success(t("friendsChat.noteSaved"));
    }
    catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      toast.error(t("friendsChat.noteSaveFailed", { error: message }));
    }
    finally {
      setSavingNote(false);
    }
  };

  const handleRemoveFriend = async () => {
    if (!deleteTarget || deletingFriend) {
      return;
    }
    setDeletingFriend(true);
    try {
      await RemoveFriend(deleteTarget.id);
      const removedID = deleteTarget.id;
      setDeleteTarget(null);
      // 正在和这个人聊天就直接退回列表，别停在一个已经没有好友关系的会话里
      if (target?.kind === "friend" && target.friend.id === removedID) {
        setTarget(null);
      }
      await refreshLists(false);
      toast.success(t("friendsChat.friendRemoved"));
    }
    catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      toast.error(t("friendsChat.friendRemoveFailed", { error: message }));
    }
    finally {
      setDeletingFriend(false);
    }
  };

  // 好友列表由后端推送驱动，前端不再自己定时拉。
  //
  // 后端每 10 秒轮询一次 /friends/list，有实质变化才把整份列表推过来
  // （见 account_friend_play.go）。以前是前端 30 秒 + 后端 15 秒各跑各的，
  // 于是会出现「列表已经显示有人在玩、通知却还没来」这种自相矛盾的状态，
  // 而且白拉一倍请求。现在界面和通知吃的是同一份快照。
  useEffect(() => {
    if (!isOpen || !isLoggedIn) {
      return;
    }
    return onWailsEvent<FriendList>(FRIEND_LIST_UPDATED_EVENT, (list) => {
      if (list?.friends) {
        setFriendList(list);
      }
    });
  }, [isOpen, isLoggedIn]);

  // 好友申请：列表接口只给「待处理条数」，申请内容要单独拉 `/friends/requests`
  // （手机版 SocialApiClient.getFriendRequests 也是这么分的 —— 以前把列表里那个
  // 数字当数组解析，所以「手机上有人加我、PC 一片空白」）。
  const loadRequests = useCallback(async () => {
    setRequestsLoading(true);
    try {
      setRequests(await ListFriendRequests());
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setRequestsLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!isOpen || !isLoggedIn || view !== "requests") {
      return;
    }
    void loadRequests();
  }, [isOpen, isLoggedIn, view, loadRequests]);

  const handleAcceptRequest = async (request: FriendRequest) => {
    try {
      await AcceptFriendRequest(request.friendshipId ?? 0, request.uid ?? 0);
      toast.success(t("friendsChat.toastAccepted"));
      // 接受之后好友列表要立刻多出这个人
      await Promise.all([loadRequests(), refreshLists(false)]);
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  };

  /** 发好友申请。成功后把这一行标成「已发送请求」——不标的话按钮还能再点，会重复申请。 */
  const handleSendFriendRequest = async (user: Friend) => {
    try {
      await SendFriendRequest(String(user.uid));
      toast.success(t("friendsChat.toastRequestSent"));
      setSearchResults(
        current =>
          current?.map(item =>
            item.uid === user.uid
              ? ({ ...item, friendStatus: "pending" } as Friend)
              : item,
          ) ?? current,
      );
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  };

  const handleRejectRequest = async (request: FriendRequest) => {
    try {
      await RejectFriendRequest(request.friendshipId ?? 0);
      toast.success(t("friendsChat.toastRejected"));
      await loadRequests();
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  };

  // 轮询清理
  useEffect(
    () => () => {
      if (pollTimerRef.current !== null) {
        window.clearInterval(pollTimerRef.current);
        pollTimerRef.current = null;
      }
    },
    [],
  );

  /** 打开会话：拉历史并启动轮询 */
  const openChat = async (nextTarget: ChatTarget) => {
    if (pollTimerRef.current !== null) {
      window.clearInterval(pollTimerRef.current);
      pollTimerRef.current = null;
    }
    setTarget(nextTarget);
    setView("chat");
    setMessages([]);
    setReplyTo(null);
    setHasMoreHistory(false);
    setHistoryLoading(true);

    try {
      const isFriend = nextTarget.kind === "friend";
      const id = isFriend ? nextTarget.friend.id : nextTarget.group.id;
      const history = isFriend
        ? await GetChatHistory(id, 0, HISTORY_PAGE_SIZE)
        : await GetGroupHistory(id, 0, HISTORY_PAGE_SIZE);
      const ordered = [...history].sort(compareMessages);
      setMessages(ordered);
      oldestIdRef.current = ordered[0]?.id ?? "";
      afterIdRef.current = ordered[ordered.length - 1]?.id ?? "";
      setHasMoreHistory(history.length >= HISTORY_PAGE_SIZE);
      scrollToBottom(false);

      // 群聊标题栏的「🟢N在线」（对齐手机版 updateOnlineCount：进群拉一次 + 轮询刷新）
      if (!isFriend) {
        void GetGroupOnlineCount(id)
          .then(count => setGroupOnlineCount(count))
          .catch(() => {
            // 静默：人数拿不到不该挡聊天
          });
      }

      let pollTick = 0;
      // 10 秒轮询新消息（与手机版一致）
      pollTimerRef.current = window.setInterval(() => {
        void (async () => {
          const current = targetRef.current;
          if (!current) {
            return;
          }
          pollTick += 1;
          try {
            if (current.kind === "friend") {
              const fresh = await PollChatMessages(
                afterIdRef.current,
                current.friend.id,
                false,
              );
              if (fresh.length > 0) {
                mergeMessages(fresh);
                afterIdRef.current
                  = fresh[fresh.length - 1].id || afterIdRef.current;
                scrollToBottom();
              }
            }
            else {
              const fresh = await PollGroupMessages(
                current.group.id,
                afterIdRef.current,
              );
              if (fresh.length > 0) {
                mergeMessages(fresh);
                afterIdRef.current
                  = fresh[fresh.length - 1].id || afterIdRef.current;
                scrollToBottom();
              }
              // 在线人数每 3 个周期（30 秒）刷一次，避免无谓请求
              if (pollTick % 3 === 0) {
                const count = await GetGroupOnlineCount(current.group.id);
                setGroupOnlineCount(count);
              }
            }
          }
          catch {
            // 轮询失败静默：下个周期再试
          }
        })();
      }, POLL_INTERVAL_MS);
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
      setView("list");
    }
    finally {
      setHistoryLoading(false);
    }
  };

  /** 加载更早的历史 */
  const loadOlderHistory = async () => {
    const current = target;
    if (!current || historyLoading || !hasMoreHistory || !oldestIdRef.current) {
      return;
    }
    setHistoryLoading(true);
    try {
      const isFriend = current.kind === "friend";
      const id = isFriend ? current.friend.id : current.group.id;
      const listEl = listRef.current;
      const previousHeight = listEl?.scrollHeight ?? 0;

      const history = isFriend
        ? await GetChatHistory(id, messages.length, HISTORY_PAGE_SIZE)
        : await GetGroupHistory(id, messages.length, HISTORY_PAGE_SIZE);

      if (history.length > 0) {
        setMessages((currentMessages) => {
          const seen = new Set(currentMessages.map(item => item.id));
          const older = history.filter(item => !seen.has(item.id));
          return [...older, ...currentMessages].sort(compareMessages);
        });
        oldestIdRef.current = history[0]?.id ?? oldestIdRef.current;
        requestAnimationFrame(() => {
          const el = listRef.current;
          if (el) {
            el.scrollTop = el.scrollHeight - previousHeight;
          }
        });
      }
      setHasMoreHistory(history.length >= HISTORY_PAGE_SIZE);
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setHistoryLoading(false);
    }
  };

  /** 发消息（私聊与群聊共用） */
  const handleSend = async () => {
    const current = target;
    const text = draft.text.trim();
    if (!current || !text || isSending) {
      return;
    }
    setIsSending(true);
    try {
      const isFriend = current.kind === "friend";
      const replyToID = replyTo?.id ?? "";
      const sent = isFriend
        ? await SendChatMessage(current.friend.id, text, "text", replyToID)
        : await SendGroupMessage(current.group.id, text, "text", replyToID);
      if (sent.id) {
        mergeMessages([sent]);
        afterIdRef.current = sent.id || afterIdRef.current;
      }
      setDraft({ text: "" });
      setReplyTo(null);
      scrollToBottom();
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsSending(false);
    }
  };

  /** 搜索用户（UID 或昵称） */
  const handleSearch = async () => {
    const keyword = searchKeyword.trim();
    if (!keyword || isSearching) {
      return;
    }
    setIsSearching(true);
    try {
      setSearchResults(await SearchUsers(keyword));
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
    finally {
      setIsSearching(false);
    }
  };

  /** 好友列表分组：正在游戏 → 在线 → 离线（Steam 风格） */
  const friendSections = useMemo(() => {
    const friends = friendList?.friends ?? [];
    const playing: Friend[] = [];
    const online: Friend[] = [];
    const offline: Friend[] = [];
    for (const friend of friends) {
      if (friend.status === "online" && friend.activity?.trim()) {
        playing.push(friend);
      }
      else if (
        friend.status === "online"
        || friend.status === "away"
        || friend.status === "busy"
      ) {
        online.push(friend);
      }
      else {
        offline.push(friend);
      }
    }
    return { playing, online, offline };
  }, [friendList]);

  if (!isOpen) {
    return null;
  }

  /**
   * 当前用户在该群能否发言。
   *
   * 对齐手机版 GroupInfo.canSpeak()：公告版（type=notice）是全体禁言频道，
   * 只有管理员能在里面发消息；私聊与普通聊天室不受限。
   */
  const canSpeak = (() => {
    if (!target || target.kind !== "group") {
      return true;
    }
    if (target.group.type !== "notice") {
      return true;
    }
    return target.group.memberRole === "admin";
  })();

  const renderAvatar = (name: string, avatar?: string, size = "h-10 w-10") => {
    if (avatar) {
      return (
        <img
          src={avatar}
          alt=""
          className={`${size} shrink-0 rounded-full border border-brand-200/70 object-cover dark:border-brand-700/70`}
        />
      );
    }
    return (
      <div
        className={`${size} flex shrink-0 items-center justify-center rounded-full bg-primary-500/15 text-sm font-semibold text-primary-600 dark:text-primary-300`}
      >
        {(name || "?").slice(0, 1).toUpperCase()}
      </div>
    );
  };

  /** 一条好友申请。actionable=true 时带「接受 / 拒绝」（只有收到的申请能操作）。 */
  const renderRequestRow = (request: FriendRequest, actionable: boolean) => (
    <div
      key={request.friendshipId}
      className="flex items-center gap-3 rounded-xl border border-brand-200/70 p-2.5 dark:border-brand-700/70"
    >
      {renderAvatar(request.nickname ?? "", request.avatar, "h-10 w-10")}
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-medium text-brand-800 dark:text-brand-100">
          {request.nickname}
        </div>
        <div className="truncate text-[11px] text-brand-500 dark:text-brand-400">
          UID
          {" "}
          {request.uid}
          {request.signature ? ` · ${request.signature}` : ""}
        </div>
      </div>
      {actionable && (
        <>
          <BetterButton
            variant="primary"
            size="sm"
            onClick={() => void handleAcceptRequest(request)}
          >
            {t("friendsChat.accept")}
          </BetterButton>
          <BetterButton
            variant="secondary"
            size="sm"
            onClick={() => void handleRejectRequest(request)}
          >
            {t("friendsChat.reject")}
          </BetterButton>
        </>
      )}
    </div>
  );

  const statusDot = (status?: string) => {
    const colorClass
      = status === "online"
        ? "bg-emerald-500"
        : status === "away" || status === "busy"
          ? "bg-amber-500"
          : "bg-brand-400 dark:bg-brand-500";
    return (
      <span className={`inline-block h-2.5 w-2.5 rounded-full ${colorClass}`} />
    );
  };

  /** 打开消息操作菜单（右键气泡 / 长按） */
  const openMessageMenu = (message: ChatMessage, x: number, y: number) => {
    setMenuMessage(message);
    // 菜单贴边时向上/向左收一点，避免超出窗口
    setMenuPosition({
      x: Math.min(x, window.innerWidth - 170),
      y: Math.min(y, window.innerHeight - 240),
    });
  };

  /** 在输入框插入 @提及（对齐手机版 mentionInGroup） */
  const insertMention = (nickname: string) => {
    const name = nickname.trim();
    if (!name) {
      return;
    }
    setDraft((previous) => {
      const separator
        = previous.text && !previous.text.endsWith(" ") ? " " : "";
      return { text: `${previous.text}${separator}@${name} ` };
    });
    // 把焦点还给输入框，方便继续打字
    requestAnimationFrame(() => {
      inputWrapRef.current?.querySelector("input")?.focus();
    });
  };

  /** 复制消息文本 */
  const copyMessageText = async (message: ChatMessage) => {
    try {
      await navigator.clipboard.writeText(message.content);
      toast.success(t("friendsChat.toastCopied"));
    }
    catch {
      toast.error(t("friendsChat.toastCopyFailed"));
    }
  };

  /** 举报消息（scene：私聊 chat / 群聊 group） */
  const submitReport = async (message: ChatMessage, reason: string) => {
    try {
      const scene = target?.kind === "group" ? "group" : "chat";
      const groupId = target?.kind === "group" ? target.group.id : "";
      await ReportChatMessage(scene, message.id, groupId, reason);
      toast.success(t("friendsChat.toastReported"));
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  };

  /** 管理员撤回 / 删除群消息 */
  const manageGroupMessage = async (
    message: ChatMessage,
    action: "recall" | "delete",
  ) => {
    try {
      await ManageGroupMessage(message.id, action);
      // 服务端处理成功后本地立即摘掉这条（撤回/删除对当前用户都等同于不可见）
      setMessages(current =>
        current.filter(item => item.id !== message.id),
      );
      toast.success(
        action === "recall"
          ? t("friendsChat.toastRecalled")
          : t("friendsChat.toastDeleted"),
      );
    }
    catch (error) {
      toast.error(error instanceof Error ? error.message : String(error));
    }
  };

  const renderMessageBubble = (message: ChatMessage, isGroup: boolean) => {
    const isMine = Boolean(message.isMine);
    const senderName = message.senderName || `UID ${message.senderUid}`;
    // 表情 / 图片 / 文本三种气泡（与手机版 buildMessageBubble 的分支一致）
    const bubbleContent = (() => {
      if (message.msgType === "emoji" || message.msgType === "image") {
        // 加载策略（代理 → 直连）与浮层共用一份实现，别再各写一套
        return (
          <ChatMessageMedia
            msgType={message.msgType}
            content={message.content}
            emojis={emojis}
          />
        );
      }
      return message.content;
    })();
    const isMedia = message.msgType === "emoji" || message.msgType === "image";
    // 群聊对齐手机版 buildGroupMessageBubble：双方都带头像，自己的在右侧（QQ 式）。
    // 自己的头像优先取消息里的（服务端可能回填），回退到本地账号状态。
    const myNickname = accountStatus?.nickname || t("friendsChat.me");
    // 私聊消息服务端**不下发** senderName/senderAvatar（与手机版一致，手机版私聊
    // 干脆不画头像）。PC 端两侧都画头像，所以这里用当前会话好友的资料兜底，
    // 否则对方的头像会退化成首字母。
    const friendLabel
      = target?.kind === "friend"
        ? target.friend.note || target.friend.nickname
        : "";
    const friendAvatar
      = target?.kind === "friend" ? target.friend.avatar : undefined;
    const avatarName = isMine
      ? senderName || myNickname
      : senderName || friendLabel || t("friendsChat.title");
    const avatarUrl = isMine
      ? message.senderAvatar || accountStatus?.avatar
      : message.senderAvatar || friendAvatar;
    return (
      <div
        key={message.id}
        className={`flex gap-2 ${isMine ? "flex-row-reverse" : "flex-row"}`}
      >
        <div className="shrink-0 pt-1">
          {(isGroup || !isMine) && (
            <ChatAvatar
              name={avatarName}
              avatar={avatarUrl}
              size={32}
              frame={message.senderFrame}
              className="cursor-pointer"
              onClick={() => {
                // 点头像看资料：他人用消息里的 senderUid；自己的消息服务端不带
                // senderUid，回退到账号 UID，这样点自己头像也能看资料。
                const uid = isMine
                  ? Number(accountStatus?.uid ?? 0)
                  : Number(message.senderUid ?? 0);
                if (uid > 0) {
                  setProfileUid(uid);
                }
              }}
              onContextMenu={() => {
                // 手机版：长按他人头像 → @ 对方（群聊才插 @）
                if (!isMine && isGroup && senderName) {
                  insertMention(senderName);
                }
              }}
            />
          )}
        </div>
        <div
          className={`flex max-w-[72%] flex-col gap-0.5 ${isMine ? "items-end" : "items-start"}`}
        >
          {isGroup && (
            <div
              className={`flex items-center gap-1 px-1 ${isMine ? "flex-row-reverse" : "flex-row"}`}
            >
              {/* 昵称颜色：服务端下发 #rrggbb，未下发时用默认色（手机版 nickColorOf） */}
              <span
                className="text-[11px] font-medium text-brand-500 dark:text-brand-400"
                style={
                  message.senderNameColor
                    ? { color: message.senderNameColor }
                    : undefined
                }
              >
                {isMine ? myNickname : senderName}
              </span>
              {/* 等级徽章（Lv.N，分档配色与手机版 levelColor 一致） */}
              {!isMine && Number(message.senderLevel) > 0 && (
                <span
                  className="rounded px-1 py-[1px] text-[9px] font-bold leading-none"
                  style={levelBadgeStyle(Number(message.senderLevel))}
                >
                  Lv.
                  {message.senderLevel}
                </span>
              )}
              {/* 管理员标识 */}
              {message.senderIsAdmin && (
                <span className="rounded border border-warning-500/40 bg-warning-500/10 px-1 py-[1px] text-[9px] font-bold leading-none text-warning-600 dark:text-warning-400">
                  {t("friendsChat.adminBadge")}
                </span>
              )}
            </div>
          )}
          {message.replyPreview && (
            <div className="max-w-full truncate rounded-md border-l-2 border-primary-400/60 bg-brand-100/70 px-2 py-0.5 text-[11px] text-brand-600 dark:bg-brand-900/50 dark:text-brand-300">
              {message.replyPreview}
            </div>
          )}
          {isMedia ? (
            <div
              className="cursor-pointer"
              title={t("friendsChat.reply")}
              onClick={() => {
                // 图片消息点击 = 全屏查看（手机版 showImageViewer）；
                // 表情点一下仍然是「回复」
                if (message.msgType === "image") {
                  setViewerImageURL(resolveChatImageURL(message.content));
                  return;
                }
                setReplyTo(message);
              }}
              onContextMenu={(event) => {
                event.preventDefault();
                openMessageMenu(message, event.clientX, event.clientY);
              }}
            >
              {bubbleContent}
            </div>
          ) : (
            <div
              className={`cursor-pointer rounded-2xl px-3 py-2 text-sm leading-relaxed ${
                isMine
                  ? "rounded-br-md bg-primary-500 text-white"
                  : "rounded-bl-md bg-brand-100 text-brand-900 dark:bg-brand-700/70 dark:text-brand-50"
              }`}
              title={t("friendsChat.reply")}
              onClick={() => setReplyTo(message)}
              onContextMenu={(event) => {
                event.preventDefault();
                openMessageMenu(message, event.clientX, event.clientY);
              }}
            >
              {bubbleContent}
            </div>
          )}
          {message.createdAt && (
            <span className="px-1 text-[10px] text-brand-400 dark:text-brand-500">
              {message.createdAt}
            </span>
          )}
        </div>
      </div>
    );
  };

  const pendingCount = friendList?.pendingCount ?? 0;

  return (
    <ModalPortal>
      <div
        className="absolute inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
        onClick={onClose}
      >
        <div
          className="flex h-[min(640px,88vh)] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-brand-200 bg-white shadow-2xl dark:border-brand-700 dark:bg-brand-800"
          onClick={e => e.stopPropagation()}
        >
          {/* 标题栏 */}
          <div className="flex items-center gap-2 border-b border-brand-200/80 px-4 py-3 dark:border-brand-700/80">
            {view !== "list" && (
              <button
                type="button"
                onClick={() => {
                  if (view === "chat" && pollTimerRef.current !== null) {
                    window.clearInterval(pollTimerRef.current);
                    pollTimerRef.current = null;
                  }
                  setView("list");
                  setTarget(null);
                  setReplyTo(null);
                  void refreshLists(false);
                }}
                aria-label={t("common.back")}
                className="rounded-lg p-1.5 text-brand-500 transition-colors hover:bg-brand-100 hover:text-brand-700 dark:text-brand-400 dark:hover:bg-brand-700"
              >
                <span className="i-mdi-arrow-left text-lg" />
              </button>
            )}
            <h2 className="flex min-w-0 flex-1 items-center gap-2 truncate text-sm font-bold text-brand-900 dark:text-white">
              {view === "chat" && target ? (
                target.kind === "friend" ? (
                  <>
                    <span className="i-mdi-chat-outline text-primary-500" />
                    <span className="truncate">
                      {target.friend.note || target.friend.nickname}
                    </span>
                  </>
                ) : (
                  <>
                    {target.group.icon ? (
                      <span className="shrink-0 text-base">
                        {target.group.icon}
                      </span>
                    ) : (
                      <span className="i-mdi-chat-processing-outline shrink-0 text-primary-500" />
                    )}
                    <span className="truncate">{target.group.name}</span>
                    {/* 在线人数只在聊天室标题栏显示（对齐手机版 updateOnlineCount）；
                        公告版不挂人数 */}
                    {target.group.type !== "notice" && (
                      <span className="shrink-0 text-[11px] font-normal text-brand-500 dark:text-brand-400">
                        {t("friendsChat.onlineCount", {
                          count: groupOnlineCount,
                        })}
                      </span>
                    )}
                  </>
                )
              ) : (
                t("friendsChat.title")
              )}
            </h2>
            <button
              type="button"
              onClick={onClose}
              aria-label={t("common.close")}
              className="rounded-lg p-1.5 text-brand-500 transition-colors hover:bg-brand-100 hover:text-brand-700 dark:text-brand-400 dark:hover:bg-brand-700"
            >
              <span className="i-mdi-close text-lg" />
            </button>
          </div>

          {/* 未登录提示 */}
          {!isLoggedIn ? (
            <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
              <span className="i-mdi-account-lock-outline text-4xl text-brand-400" />
              <p className="text-sm text-brand-600 dark:text-brand-300">
                {t("friendsChat.loginRequired")}
              </p>
            </div>
          ) : (
            view === "list" && (
              <div className="flex min-h-0 flex-1 flex-col">
                {/* 操作栏 */}
                <div className="flex gap-2 px-4 pt-3">
                  <BetterButton
                    variant="secondary"
                    size="sm"
                    icon="i-mdi-account-plus-outline"
                    className="flex-1"
                    onClick={() => setView("add")}
                  >
                    {t("friendsChat.addFriend")}
                  </BetterButton>
                  <BetterButton
                    variant="secondary"
                    size="sm"
                    icon="i-mdi-email-outline"
                    className="flex-1"
                    onClick={() => setView("requests")}
                  >
                    {pendingCount > 0
                      ? t("friendsChat.requests", { count: pendingCount })
                      : t("friendsChat.requestsZero")}
                  </BetterButton>
                </div>

                <div className="mt-3 min-h-0 flex-1 overflow-y-auto scrollbar-thin px-4 pb-4">
                  {isLoading && !friendList && (
                    <p className="py-8 text-center text-sm text-brand-500">
                      {t("friendsChat.loading")}
                    </p>
                  )}
                  {loadError && (
                    <div className="flex flex-col items-center gap-2 py-8">
                      <p className="text-sm text-error-500">{loadError}</p>
                      <BetterButton
                        variant="secondary"
                        size="sm"
                        onClick={() => void refreshLists(true)}
                      >
                        {t("common.retry")}
                      </BetterButton>
                    </div>
                  )}

                  {friendList && (
                    <>
                      {/* 群组分区 */}
                      {groups.length > 0 && (
                        <>
                          <p className="mb-1.5 mt-1 text-[11px] font-semibold uppercase tracking-wide text-brand-400 dark:text-brand-500">
                            {t("friendsChat.groups")}
                            {" "}
                            —
                            {groups.length}
                          </p>
                          <div className="mb-3 flex flex-col gap-1">
                            {groups.map((group) => {
                              // 与手机版 buildGroupRow 一致：副行是群描述，
                              // 没有描述时按类型给默认文案（公告版=仅管理员可发言）；
                              // 公告版额外挂一个「公告」小标签。**不显示人数**。
                              const isNotice = group.type === "notice";
                              const desc
                                = group.description
                                  || (isNotice
                                    ? t("friendsChat.groupNoticeDesc")
                                    : t("friendsChat.groupChatDesc"));
                              return (
                                <button
                                  key={group.id}
                                  type="button"
                                  onClick={() =>
                                    void openChat({ kind: "group", group })}
                                  className="flex items-center gap-3 rounded-xl p-2 text-left transition-colors hover:bg-brand-100 dark:hover:bg-brand-700/60"
                                >
                                  {group.icon ? (
                                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full border border-brand-200/70 bg-brand-100/80 text-lg dark:border-brand-700/70 dark:bg-brand-700/50">
                                      {group.icon}
                                    </div>
                                  ) : (
                                    renderAvatar(group.name, group.avatar)
                                  )}
                                  <div className="min-w-0 flex-1">
                                    <div className="truncate text-sm font-medium text-brand-800 dark:text-brand-100">
                                      {group.name}
                                    </div>
                                    <div className="truncate text-[11px] text-brand-500 dark:text-brand-400">
                                      {desc}
                                    </div>
                                  </div>
                                  {isNotice && (
                                    <span className="shrink-0 rounded-md border border-warning-500/40 bg-warning-500/10 px-1.5 py-0.5 text-[10px] font-medium text-warning-600 dark:text-warning-400">
                                      {t("friendsChat.groupNoticeBadge")}
                                    </span>
                                  )}
                                  {group.unreadCount > 0 && (
                                    <span className="min-w-[18px] rounded-full bg-error-500 px-1.5 py-0.5 text-center text-[10px] font-bold text-white">
                                      {group.unreadCount > 99
                                        ? "99+"
                                        : group.unreadCount}
                                    </span>
                                  )}
                                </button>
                              );
                            })}
                          </div>
                        </>
                      )}

                      {/* 好友分区 */}
                      {friendSections.playing.length === 0
                        && friendSections.online.length === 0
                        && friendSections.offline.length === 0 && (
                        <div className="flex flex-col items-center gap-2 py-10 text-center">
                          <span className="i-mdi-account-multiple-outline text-4xl text-brand-300 dark:text-brand-600" />
                          <p className="whitespace-pre-line text-sm text-brand-500 dark:text-brand-400">
                            {t("friendsChat.noFriends")}
                          </p>
                        </div>
                      )}

                      {(
                        [
                          ["playing", friendSections.playing],
                          ["online", friendSections.online],
                          ["offline", friendSections.offline],
                        ] as const
                      ).map(([key, list]) =>
                        list.length > 0 ? (
                          <div key={key}>
                            <p className="mb-1.5 mt-2 text-[11px] font-semibold uppercase tracking-wide text-brand-400 dark:text-brand-500">
                              {t(`friendsChat.section.${key}`)}
                              {" "}
                              —
                              {list.length}
                            </p>
                            <div className="flex flex-col gap-1">
                              {list.map(friend => (
                                <button
                                  key={friend.id}
                                  type="button"
                                  onClick={() =>
                                    void openChat({ kind: "friend", friend })}
                                  onContextMenu={(event) => {
                                    // 点整行照旧进私聊；右键才出「查看资料 / 发消息」
                                    event.preventDefault();
                                    setFriendMenu(friend);
                                    setFriendMenuPosition({
                                      x: event.clientX,
                                      y: event.clientY,
                                    });
                                  }}
                                  className="flex items-center gap-3 rounded-xl p-2 text-left transition-colors hover:bg-brand-100 dark:hover:bg-brand-700/60"
                                >
                                  <div className="relative">
                                    {renderAvatar(
                                      friend.note || friend.nickname,
                                      friend.avatar,
                                    )}
                                    <span className="absolute -bottom-0.5 -right-0.5">
                                      {statusDot(friend.status)}
                                    </span>
                                  </div>
                                  <div className="min-w-0 flex-1">
                                    <div className="truncate text-sm font-medium text-brand-800 dark:text-brand-100">
                                      {friend.note || friend.nickname}
                                      {friend.note && (
                                        <span className="ml-1 text-[11px] font-normal text-brand-400">
                                          {friend.nickname}
                                        </span>
                                      )}
                                    </div>
                                    <div className="flex min-w-0 items-center gap-1 text-[11px] text-brand-500 dark:text-brand-400">
                                      {/* 平台图标（手机 / 电脑 / 网页）：离线不显示 */}
                                      {showsPresencePlatform(friend.status) && (
                                        <PresencePlatformIcon
                                          platform={friend.platform}
                                          size={13}
                                          className="shrink-0"
                                          title={t(
                                            presencePlatformLabelKey(
                                              friend.platform,
                                            ),
                                          )}
                                        />
                                      )}
                                      <span className="truncate">
                                        {friend.activity
                                          || friend.lastMessage
                                          || t(
                                            `friendsChat.status.${friend.status || "offline"}`,
                                          )}
                                      </span>
                                    </div>
                                  </div>
                                  {friend.unreadCount > 0 && (
                                    <span className="min-w-[18px] rounded-full bg-error-500 px-1.5 py-0.5 text-center text-[10px] font-bold text-white">
                                      {friend.unreadCount > 99
                                        ? "99+"
                                        : friend.unreadCount}
                                    </span>
                                  )}
                                </button>
                              ))}
                            </div>
                          </div>
                        ) : null,
                      )}
                    </>
                  )}
                </div>
              </div>
            )
          )}

          {isLoggedIn && view === "requests" && (
            <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin p-4">
              <p className="mb-3 text-sm font-semibold text-brand-800 dark:text-brand-100">
                {t("friendsChat.requestsTitle")}
              </p>

              {requestsLoading && requests === null ? (
                <p className="py-8 text-center text-sm text-brand-500 dark:text-brand-400">
                  {t("friendsChat.loading")}
                </p>
              ) : (
                <>
                  <p className="mb-2 text-xs font-semibold tracking-wide text-brand-500 dark:text-brand-400">
                    {t("friendsChat.requestsIncoming")}
                  </p>
                  {(requests?.incoming?.length ?? 0) === 0 ? (
                    <p className="py-6 text-center text-sm text-brand-500 dark:text-brand-400">
                      {t("friendsChat.noRequests")}
                    </p>
                  ) : (
                    <div className="flex flex-col gap-2">
                      {requests?.incoming?.map(request =>
                        renderRequestRow(request, true),
                      )}
                    </div>
                  )}

                  {(requests?.outgoing?.length ?? 0) > 0 && (
                    <>
                      <p className="mt-4 mb-2 text-xs font-semibold tracking-wide text-brand-500 dark:text-brand-400">
                        {t("friendsChat.requestsOutgoing")}
                      </p>
                      <div className="flex flex-col gap-2">
                        {requests?.outgoing?.map(request =>
                          renderRequestRow(request, false),
                        )}
                      </div>
                    </>
                  )}
                </>
              )}
            </div>
          )}

          {isLoggedIn && view === "add" && (
            <div className="min-h-0 flex-1 overflow-y-auto scrollbar-thin p-4">
              <p className="mb-3 text-sm font-semibold text-brand-800 dark:text-brand-100">
                {t("friendsChat.addFriendTitle")}
              </p>
              <div className="flex gap-2">
                <BetterInput
                  value={searchKeyword}
                  onChange={e => setSearchKeyword(e.target.value)}
                  placeholder={t("friendsChat.searchPlaceholder")}
                  fullWidth
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      void handleSearch();
                    }
                  }}
                />
                <BetterButton
                  variant="primary"
                  className="shrink-0"
                  isLoading={isSearching}
                  onClick={() => void handleSearch()}
                >
                  {t("friendsChat.search")}
                </BetterButton>
              </div>

              <div className="mt-4 flex flex-col gap-2">
                {searchResults?.length === 0 && (
                  <p className="py-6 text-center text-sm text-brand-500 dark:text-brand-400">
                    {t("friendsChat.noSearchResults")}
                  </p>
                )}
                {searchResults?.map((user) => {
                  // 与手机版 renderSearchResults 同一套判断：已是好友 / 申请中都不该
                  // 再显示可点的「加好友」（点了服务端也不会重复受理）
                  const friendStatus = user.friendStatus || "none";
                  return (
                    <div
                      key={user.id}
                      className="flex items-center gap-3 rounded-xl border border-brand-200/70 p-2.5 dark:border-brand-700/70"
                    >
                      {renderAvatar(user.nickname, user.avatar)}
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium text-brand-800 dark:text-brand-100">
                          {user.nickname}
                        </div>
                        <div className="truncate text-[11px] text-brand-500 dark:text-brand-400">
                          UID
                          {" "}
                          {user.uid}
                          {user.signature ? ` · ${user.signature}` : ""}
                        </div>
                      </div>
                      {friendStatus === "accepted" ? (
                        <span className="shrink-0 text-[11px] font-medium text-emerald-600 dark:text-emerald-400">
                          {t("friendsChat.alreadyFriend")}
                        </span>
                      ) : friendStatus === "pending" ? (
                        <span className="shrink-0 text-[11px] font-medium text-brand-500 dark:text-brand-400">
                          {t("friendsChat.requestPending")}
                        </span>
                      ) : (
                        <BetterButton
                          variant="primary"
                          size="sm"
                          icon="i-mdi-account-plus"
                          onClick={() => void handleSendFriendRequest(user)}
                        >
                          {t("friendsChat.sendRequest")}
                        </BetterButton>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {isLoggedIn && view === "chat" && target && (
            <div className="flex min-h-0 flex-1 flex-col">
              <div
                ref={listRef}
                className="min-h-0 flex-1 space-y-3 overflow-y-auto scrollbar-thin p-4"
                onScroll={(e) => {
                  // 手机版行为：滚动到顶部自动翻更早的历史
                  const el = e.currentTarget;
                  if (el.scrollTop <= 32 && hasMoreHistory && !historyLoading) {
                    void loadOlderHistory();
                  }
                }}
              >
                {hasMoreHistory && (
                  <div className="flex justify-center">
                    <BetterButton
                      variant="secondary"
                      size="sm"
                      isLoading={historyLoading}
                      onClick={() => void loadOlderHistory()}
                    >
                      {t("friendsChat.loadOlder")}
                    </BetterButton>
                  </div>
                )}
                {historyLoading && messages.length === 0 && (
                  <p className="py-8 text-center text-sm text-brand-500">
                    {t("friendsChat.loading")}
                  </p>
                )}
                {!historyLoading && messages.length === 0 && (
                  <p className="py-8 text-center text-sm text-brand-500 dark:text-brand-400">
                    {t("friendsChat.startChat")}
                  </p>
                )}
                {messages.map(message =>
                  renderMessageBubble(message, target.kind === "group"),
                )}
              </div>

              {/* 回复预览 */}
              {replyTo && (
                <div className="flex items-center gap-2 border-t border-brand-200/70 px-4 py-2 dark:border-brand-700/70">
                  <span className="i-mdi-reply text-brand-400" />
                  <span className="min-w-0 flex-1 truncate text-[11px] text-brand-500 dark:text-brand-400">
                    {replyTo.senderName || `UID ${replyTo.senderUid}`}
                    {": "}
                    {replyTo.content}
                  </span>
                  <button
                    type="button"
                    onClick={() => setReplyTo(null)}
                    aria-label={t("common.cancel")}
                    className="text-brand-400 hover:text-brand-600 dark:hover:text-brand-300"
                  >
                    <span className="i-mdi-close text-sm" />
                  </button>
                </div>
              )}

              {/* 表情 / 贴纸选择面板（与手机版双 tab 一致） */}
              {emojiPanelOpen && (
                <div className="border-t border-brand-200/80 bg-brand-50/80 p-3 dark:border-brand-700/80 dark:bg-brand-900/40">
                  <div className="mb-2 flex items-center gap-2">
                    {emojiTab === 2 ? (
                      <button
                        type="button"
                        onClick={() => setEmojiTab(1)}
                        className="text-xs text-primary-600 hover:underline dark:text-primary-300"
                      >
                        {t("friendsChat.backToPacks")}
                      </button>
                    ) : (
                      <>
                        <button
                          type="button"
                          onClick={() => {
                            setEmojiTab(0);
                            void loadEmojiPanelData();
                          }}
                          className={`rounded-lg px-3 py-1 text-xs font-medium transition-colors ${
                            emojiTab === 0
                              ? "bg-primary-500 text-white"
                              : "text-brand-600 hover:bg-brand-100 dark:text-brand-300 dark:hover:bg-brand-700/60"
                          }`}
                        >
                          {t("friendsChat.emojiTabLocal")}
                        </button>
                        {stickerEnabled && (
                          <button
                            type="button"
                            onClick={() => {
                              setEmojiTab(1);
                              void loadEmojiPanelData();
                            }}
                            className={`rounded-lg px-3 py-1 text-xs font-medium transition-colors ${
                              emojiTab === 1
                                ? "bg-primary-500 text-white"
                                : "text-brand-600 hover:bg-brand-100 dark:text-brand-300 dark:hover:bg-brand-700/60"
                            }`}
                          >
                            {t("friendsChat.emojiTabStickers")}
                          </button>
                        )}
                      </>
                    )}
                    <button
                      type="button"
                      onClick={() => setEmojiPanelOpen(false)}
                      aria-label={t("common.close")}
                      className="ml-auto rounded-lg p-1.5 text-brand-400 transition-colors hover:bg-brand-100 hover:text-brand-600 dark:hover:bg-brand-700"
                    >
                      <span className="i-mdi-close text-sm" />
                    </button>
                  </div>

                  {stickerLoading && (
                    <p className="py-6 text-center text-xs text-brand-500">
                      {t("friendsChat.loading")}
                    </p>
                  )}

                  {/* tab 0：本站表情网格 */}
                  {emojiTab === 0 && (
                    <div className="grid max-h-48 grid-cols-6 gap-1 overflow-y-auto sm:grid-cols-8">
                      {emojis === null && (
                        <p className="col-span-full py-4 text-center text-xs text-brand-500">
                          {t("friendsChat.loading")}
                        </p>
                      )}
                      {emojis?.length === 0 && (
                        <p className="col-span-full py-4 text-center text-xs text-brand-500 dark:text-brand-400">
                          {t("friendsChat.noEmojis")}
                        </p>
                      )}
                      {emojis?.map(emoji => (
                        <button
                          key={emoji.name}
                          type="button"
                          title={emoji.name}
                          onClick={() => void sendEmoji(emoji.name)}
                          className="flex items-center justify-center rounded-lg p-1 transition-colors hover:bg-brand-100 dark:hover:bg-brand-700/60"
                        >
                          <img
                            src={proxiedImageSrc(
                              emoji.url || emojiDisplayURL(emoji.name),
                            )}
                            alt={emoji.name}
                            loading="lazy"
                            className="h-9 w-9 object-contain"
                            onError={(e) => {
                              const img = e.currentTarget;
                              const raw
                                = emoji.url || emojiDisplayURL(emoji.name);
                              if (!img.dataset.fallbackRaw && img.src !== raw) {
                                img.dataset.fallbackRaw = "1";
                                img.src = raw;
                              }
                            }}
                          />
                        </button>
                      ))}
                    </div>
                  )}

                  {/* tab 1：未萌贴纸包列表 */}
                  {emojiTab === 1 && (
                    <div className="grid max-h-48 grid-cols-3 gap-2 overflow-y-auto sm:grid-cols-4">
                      {stickerPacks.map(pack => (
                        <button
                          key={pack.id}
                          type="button"
                          onClick={() => void openStickerPack(pack)}
                          className="flex items-center gap-2 rounded-xl border border-brand-200/70 p-1.5 text-left transition-colors hover:bg-brand-100 dark:border-brand-700/70 dark:hover:bg-brand-700/60"
                        >
                          <img
                            src={proxiedImageSrc(pack.cover)}
                            alt=""
                            className="h-10 w-10 shrink-0 rounded-lg object-cover"
                          />
                          <span className="min-w-0 flex-1 truncate text-[11px] font-medium text-brand-700 dark:text-brand-200">
                            {pack.title}
                          </span>
                        </button>
                      ))}
                    </div>
                  )}

                  {/* tab 2：包内表情网格（点击直接发 URL） */}
                  {emojiTab === 2 && activeStickerPack && (
                    <div className="grid max-h-48 grid-cols-6 gap-1 overflow-y-auto sm:grid-cols-8">
                      {stickerURLs.map(url => (
                        <button
                          key={url}
                          type="button"
                          onClick={() => void sendEmoji(url)}
                          className="flex items-center justify-center rounded-lg p-1 transition-colors hover:bg-brand-100 dark:hover:bg-brand-700/60"
                        >
                          <img
                            src={proxiedImageSrc(url)}
                            alt=""
                            loading="lazy"
                            className="h-9 w-9 object-contain"
                            onError={(e) => {
                              // 一次性把整包塞进代理会打满并发图片请求（本地代理逐个回源），
                              // 失败的那几张退回直连再拿一次。
                              const img = e.currentTarget;
                              if (!img.dataset.fallbackRaw && img.src !== url) {
                                img.dataset.fallbackRaw = "1";
                                img.src = url;
                              }
                            }}
                          />
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {/* 输入区（公告版：仅管理员可发言，对齐手机版 canSpeak） */}
              {!canSpeak ? (
                <div className="flex items-center justify-center gap-2 border-t border-brand-200/80 p-3 dark:border-brand-700/80">
                  <span className="i-mdi-bullhorn-outline text-base text-warning-500" />
                  <span className="text-xs text-brand-500 dark:text-brand-400">
                    {t("friendsChat.groupNoticeMuted")}
                  </span>
                </div>
              ) : (
                <div
                  ref={inputWrapRef}
                  className="flex items-center gap-2 border-t border-brand-200/80 p-3 dark:border-brand-700/80"
                >
                  <button
                    type="button"
                    onClick={() => {
                      setEmojiPanelOpen(open => !open);
                      void loadEmojiPanelData();
                    }}
                    aria-label={t("friendsChat.emojiPicker")}
                    className={`shrink-0 rounded-lg p-2 transition-colors ${
                      emojiPanelOpen
                        ? "bg-primary-500/15 text-primary-600 dark:text-primary-300"
                        : "text-brand-500 hover:bg-brand-100 hover:text-brand-700 dark:text-brand-400 dark:hover:bg-brand-700"
                    }`}
                  >
                    <span className="inline-block i-mdi-emoticon-outline text-lg" />
                  </button>
                  <button
                    type="button"
                    onClick={() => void pickAndSendImage()}
                    disabled={isUploadingImage}
                    aria-label={t("friendsChat.sendImage")}
                    className="shrink-0 rounded-lg p-2 text-brand-500 transition-colors hover:bg-brand-100 hover:text-brand-700 disabled:opacity-50 dark:text-brand-400 dark:hover:bg-brand-700"
                  >
                    <span
                      className={`inline-block ${isUploadingImage ? "i-mdi-loading animate-spin" : "i-mdi-image-outline"} text-lg`}
                    />
                  </button>
                  <BetterInput
                    value={draft.text}
                    onChange={e => setDraft({ text: e.target.value })}
                    placeholder={t("friendsChat.inputPlaceholder")}
                    fullWidth
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !e.shiftKey) {
                        e.preventDefault();
                        void handleSend();
                      }
                    }}
                  />
                  <BetterButton
                    variant="primary"
                    className="shrink-0"
                    icon="i-mdi-send"
                    isLoading={isSending}
                    disabled={!draft.text.trim()}
                    onClick={() => void handleSend()}
                  />
                </div>
              )}
            </div>
          )}
        </div>
      </div>
      {/* 消息操作菜单（复制 / 回复 / 举报 / 资料 / 管理撤回删除） */}
      <ChatMessageMenu
        isOpen={Boolean(menuMessage)}
        message={menuMessage}
        position={menuPosition}
        isGroup={target?.kind === "group"}
        isGroupAdmin={
          target?.kind === "group" && target.group.memberRole === "admin"
        }
        onClose={() => setMenuMessage(null)}
        onCopy={() => {
          if (menuMessage) {
            void copyMessageText(menuMessage);
          }
        }}
        onReply={() => {
          if (menuMessage) {
            setReplyTo(menuMessage);
          }
        }}
        onReport={(reason) => {
          if (menuMessage) {
            void submitReport(menuMessage, reason);
          }
        }}
        onRecall={() => {
          if (menuMessage) {
            void manageGroupMessage(menuMessage, "recall");
          }
        }}
        onDelete={() => {
          if (menuMessage) {
            void manageGroupMessage(menuMessage, "delete");
          }
        }}
        onViewProfile={() => {
          if (menuMessage?.senderUid) {
            setProfileUid(Number(menuMessage.senderUid));
          }
        }}
      />

      {/* 好友列表项右键菜单：点整行照旧进私聊，这里补「查看资料 / 发消息」 */}
      <ContextMenu
        position={friendMenu ? friendMenuPosition : null}
        onClose={() => setFriendMenu(null)}
        items={[
          {
            key: "profile",
            label: t("friendsChat.menuProfile"),
            icon: "i-mdi-account-details-outline",
            onSelect: () => {
              if (Number(friendMenu?.uid ?? 0) > 0) {
                setProfileUid(Number(friendMenu?.uid));
              }
            },
          },
          {
            key: "message",
            label: t("friendsChat.profileSendMessage"),
            icon: "i-mdi-message-text-outline",
            onSelect: () => {
              if (friendMenu) {
                void openChat({ kind: "friend", friend: friendMenu });
              }
            },
          },
          {
            key: "note",
            label: t("friendsChat.menuSetNote"),
            icon: "i-mdi-note-edit-outline",
            onSelect: () => {
              if (friendMenu) {
                openNoteEditor(friendMenu);
              }
            },
          },
          {
            key: "remove",
            label: t("friendsChat.menuRemoveFriend"),
            icon: "i-mdi-account-remove-outline",
            danger: true,
            onSelect: () => {
              if (friendMenu) {
                setDeleteTarget(friendMenu);
              }
            },
          },
        ]}
      />

      {/* 用户资料页（点他人头像 / 菜单里「查看资料」） */}
      <UserProfileModal
        isOpen={profileUid > 0}
        uid={profileUid}
        onClose={() => setProfileUid(0)}
        onMessage={(targetUid) => {
          // 资料卡里点「发消息」：切到与该好友的私聊
          const friend = friendList?.friends?.find(
            item => Number(item.uid) === Number(targetUid),
          );
          if (friend) {
            void openChat({ kind: "friend", friend });
          }
          setProfileUid(0);
        }}
      />

      {/* 图片全屏查看 */}
      <ImageViewerModal
        isOpen={Boolean(viewerImageURL)}
        url={viewerImageURL}
        onClose={() => setViewerImageURL("")}
      />

      {/* 好友备注（手机版：长按好友 → 设置备注，最多 50 字） */}
      {noteTarget && (
        <ModalPortal>
          <div className="absolute inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm">
            <div className="w-full max-w-md rounded-xl border border-brand-200 bg-white p-6 shadow-xl dark:border-brand-700 dark:bg-brand-800">
              <h3 className="mb-1 text-lg font-bold text-brand-900 dark:text-white">
                {t("friendsChat.menuSetNote")}
              </h3>
              <p className="mb-4 text-sm text-brand-600 dark:text-brand-400">
                {t("friendsChat.noteHint", {
                  name: noteTarget.nickname,
                  max: FRIEND_NOTE_MAX_LENGTH,
                })}
              </p>
              <BetterInput
                value={noteDraft}
                maxLength={FRIEND_NOTE_MAX_LENGTH}
                placeholder={noteTarget.nickname}
                onChange={event => setNoteDraft(event.target.value)}
              />
              <div className="mt-1 text-right text-xs text-brand-500 dark:text-brand-400">
                {noteDraft.length}
                /
                {FRIEND_NOTE_MAX_LENGTH}
              </div>
              <div className="mt-6 flex justify-end gap-3">
                <BetterButton
                  type="button"
                  variant="secondary"
                  onClick={() => setNoteTarget(null)}
                >
                  {t("common.cancel")}
                </BetterButton>
                <BetterButton
                  type="button"
                  variant="primary"
                  isLoading={savingNote}
                  onClick={() => void handleSaveNote()}
                >
                  {t("common.save")}
                </BetterButton>
              </div>
            </div>
          </div>
        </ModalPortal>
      )}

      {/* 删除好友（不可撤销，给一次确认） */}
      <ConfirmModal
        isOpen={Boolean(deleteTarget)}
        type="danger"
        title={t("friendsChat.menuRemoveFriend")}
        message={t("friendsChat.removeFriendConfirm", {
          name: deleteTarget?.note || deleteTarget?.nickname || "",
        })}
        confirmText={t("friendsChat.menuRemoveFriend")}
        onConfirm={() => void handleRemoveFriend()}
        onClose={() => setDeleteTarget(null)}
      />
    </ModalPortal>
  );
}
