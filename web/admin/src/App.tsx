import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { AdminApi } from "./api";
import { EmptyState, LoadingRows } from "./components/EmptyState";
import { NoticeBanner } from "./components/NoticeBanner";
import type { AdminNotice, AdminView, Conversation, Sticker } from "./types";
import { noticeForError } from "./utils/errors";
import { AttemptsView } from "./views/AttemptsView";
import { ConversationView } from "./views/ConversationView";
import { NotesView } from "./views/NotesView";
import { StickersView } from "./views/StickersView";

type LoadState = "idle" | "loading" | "refreshing" | "ready" | "error";

interface ViewMeta {
  id: AdminView;
  label: string;
  shortLabel: string;
  description: string;
}

const VIEWS: ViewMeta[] = [
  { id: "conversation", label: "最近对话", shortLabel: "话", description: "消息与上下文" },
  { id: "notes", label: "相处笔记", shortLabel: "记", description: "长期记忆" },
  { id: "attempts", label: "回应记录", shortLabel: "应", description: "检索与决策" },
  { id: "stickers", label: "全局表情包", shortLabel: "表", description: "跨群素材库" },
];

function App() {
  const [token, setToken] = useState("");
  const [api, setApi] = useState(() => new AdminApi(""));
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [groups, setGroups] = useState<number[]>([]);
  const [selectedGroup, setSelectedGroup] = useState<number | null>(null);
  const [view, setView] = useState<AdminView>("conversation");
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [conversationGroup, setConversationGroup] = useState<number | null>(null);
  const [conversationState, setConversationState] = useState<LoadState>("idle");
  const [stickers, setStickers] = useState<Sticker[]>([]);
  const [stickersState, setStickersState] = useState<LoadState>("idle");
  const [notice, setNotice] = useState<AdminNotice | null>(null);

  const conversationRequest = useRef<AbortController | null>(null);
  const stickersRequest = useRef<AbortController | null>(null);

  const showError = useCallback((error: unknown, fallback: string) => {
    setNotice(noticeForError(error, fallback));
  }, []);

  const showSuccess = useCallback((message: string) => {
    setNotice({ tone: "success", message });
  }, []);

  const loadConversation = useCallback(async (client: AdminApi, group: number, preserve = false) => {
    conversationRequest.current?.abort();
    const controller = new AbortController();
    conversationRequest.current = controller;

    if (!preserve) {
      setConversation(null);
      setConversationState("loading");
    } else {
      setConversationState("refreshing");
    }
    setConversationGroup(group);

    try {
      const data = await client.getConversation(String(group), controller.signal);
      if (controller.signal.aborted) {
        return;
      }
      setConversation(data);
      setConversationState("ready");
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      setConversationState("error");
      showError(error, "读取对话数据失败，请稍后重试。");
    }
  }, [showError]);

  const loadStickers = useCallback(async (client: AdminApi, preserve = false) => {
    stickersRequest.current?.abort();
    const controller = new AbortController();
    stickersRequest.current = controller;

    if (!preserve) {
      setStickers([]);
      setStickersState("loading");
    } else {
      setStickersState("refreshing");
    }

    try {
      const data = await client.getStickers(controller.signal);
      if (controller.signal.aborted) {
        return;
      }
      setStickers(data);
      setStickersState("ready");
    } catch (error) {
      if (controller.signal.aborted) {
        return;
      }
      setStickersState("error");
      showError(error, "读取表情包失败，请稍后重试。");
    }
  }, [showError]);

  useEffect(() => () => {
    conversationRequest.current?.abort();
    stickersRequest.current?.abort();
  }, []);

  async function handleConnect(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setConnecting(true);
    setNotice(null);

    const nextApi = new AdminApi(token);
    try {
      const nextGroups = await nextApi.getGroups();
      const nextGroup = selectedGroup !== null && nextGroups.includes(selectedGroup)
        ? selectedGroup
        : nextGroups[0] ?? null;

      setApi(nextApi);
      setConnected(true);
      setGroups(nextGroups);
      setSelectedGroup(nextGroup);
      setConversation(null);
      setConversationGroup(nextGroup);

      const tasks: Promise<void>[] = [];
      if (nextGroup !== null) {
        tasks.push(loadConversation(nextApi, nextGroup));
      } else {
        setConversationState("ready");
      }
      if (view === "stickers") {
        tasks.push(loadStickers(nextApi));
      }
      setNotice({
        tone: "success",
        message: nextGroups.length === 0 ? "已连接，暂无可管理的群记录。" : "后台已连接。",
      });
      await Promise.all(tasks);
    } catch (error) {
      showError(error, "连接后台失败，请检查服务状态和访问令牌。");
    } finally {
      setConnecting(false);
    }
  }

  function disconnect() {
    conversationRequest.current?.abort();
    stickersRequest.current?.abort();
    setToken("");
    setApi(new AdminApi(""));
    setConnected(false);
    setGroups([]);
    setSelectedGroup(null);
    setConversation(null);
    setConversationGroup(null);
    setConversationState("idle");
    setStickers([]);
    setStickersState("idle");
    setNotice({ tone: "info", message: "已断开后台连接，访问令牌已从当前页面清除。" });
  }

  function handleGroupChange(value: string) {
    const group = Number(value);
    if (!Number.isSafeInteger(group) || group <= 0) {
      return;
    }
    setSelectedGroup(group);
    setConversation(null);
    if (connected) {
      void loadConversation(api, group);
    }
  }

  function selectView(nextView: AdminView) {
    setView(nextView);
    if (nextView === "stickers" && connected && (stickersState === "idle" || stickersState === "error") && stickers.length === 0) {
      void loadStickers(api);
    }
  }

  async function refreshCurrentView() {
    if (!connected) {
      return;
    }
    if (view === "stickers") {
      await loadStickers(api, true);
    } else if (selectedGroup !== null) {
      await loadConversation(api, selectedGroup, true);
    }
  }

  const refreshing = conversationState === "loading"
    || conversationState === "refreshing"
    || stickersState === "loading"
    || stickersState === "refreshing";

  const navCounts: Record<AdminView, number | undefined> = {
    conversation: conversationGroup === selectedGroup && conversation ? conversation.messages.length : undefined,
    notes: conversationGroup === selectedGroup && conversation ? conversation.notes.length : undefined,
    attempts: conversationGroup === selectedGroup && conversation ? conversation.attempts.length : undefined,
    stickers: stickersState === "ready" || stickers.length > 0 ? stickers.length : undefined,
  };

  function renderCurrentView() {
    if (!connected) {
      return (
        <div className="welcome-panel">
          <span className="welcome-mark" aria-hidden="true">群</span>
          <div>
            <p className="eyebrow">本机运维入口</p>
            <h2>连接后台后开始查看</h2>
            <p>输入访问令牌；未配置令牌的本机服务可以直接连接。令牌只保存在当前页面内存中。</p>
          </div>
        </div>
      );
    }

    if (view !== "stickers" && selectedGroup === null) {
      return (
        <EmptyState
          title="暂无可查看的群"
          description="数据库中还没有群记录。机器人接入群并产生消息后，这里会出现可选群。"
        />
      );
    }

    if (view === "conversation") {
      return (
        <ConversationView
          messages={conversation?.messages ?? []}
          loading={conversationState === "loading" || conversationState === "refreshing"}
        />
      );
    }

    if (view === "notes" && selectedGroup !== null) {
      return (
        <NotesView
          api={api}
          group={selectedGroup}
          notes={conversation?.notes ?? []}
          loading={conversationState === "loading" || conversationState === "refreshing"}
          onReload={() => loadConversation(api, selectedGroup, true)}
          onError={showError}
          onSuccess={showSuccess}
        />
      );
    }

    if (view === "attempts") {
      return (
        <AttemptsView
          attempts={conversation?.attempts ?? []}
          loading={conversationState === "loading" || conversationState === "refreshing"}
        />
      );
    }

    return (
      <StickersView
        api={api}
        stickers={stickers}
        loading={stickersState === "loading" || stickersState === "refreshing"}
        onReload={() => loadStickers(api, true)}
        onError={showError}
        onSuccess={showSuccess}
      />
    );
  }

  const currentView = VIEWS.find((item) => item.id === view) ?? VIEWS[0];

  return (
    <div className="app-shell">
      <header className="app-header">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">群</span>
          <div>
            <p className="eyebrow">GO AGENT ADMIN</p>
            <h1>群友 · 运行与记忆</h1>
            <p className="brand-subtitle">查看真实对话、检索结果和长期记忆，纠正会影响后续回应的数据。</p>
          </div>
        </div>
        <div className="header-actions">
          <span className={`connection-pill ${connected ? "is-online" : ""}`}>
            <span aria-hidden="true" />
            {connecting ? "连接中" : connected ? "已连接" : "未连接"}
          </span>
          <button
            className="button button-secondary"
            type="button"
            disabled={!connected || refreshing || (view !== "stickers" && selectedGroup === null)}
            onClick={() => void refreshCurrentView()}
          >
            {refreshing ? "刷新中…" : "刷新数据"}
          </button>
        </div>
      </header>

      <div className="workspace">
        <aside className="sidebar" aria-label="后台导航与连接">
          <section className="sidebar-card connection-card">
            <div className="sidebar-heading">
              <div>
                <p className="eyebrow">连接</p>
                <h2>服务访问</h2>
              </div>
              {connected ? (
                <button className="text-button" type="button" onClick={disconnect}>断开</button>
              ) : null}
            </div>
            <form className="connection-form" onSubmit={handleConnect}>
              <label className="field">
                <span>访问令牌</span>
                <input
                  type="password"
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="本机未配置时可留空"
                />
              </label>
              <button className="button button-primary button-wide" type="submit" disabled={connecting}>
                {connecting ? "连接中…" : connected ? "重新连接" : "连接后台"}
              </button>
            </form>
            <label className="field group-field">
              <span>当前群</span>
              <select
                value={selectedGroup ?? ""}
                disabled={!connected || groups.length === 0}
                onChange={(event) => handleGroupChange(event.target.value)}
              >
                {groups.length === 0 ? <option value="">暂无群记录</option> : null}
                {groups.map((group) => <option value={group} key={group}>群 {group}</option>)}
              </select>
            </label>
            <p className="privacy-note">令牌不会写入本地存储；刷新页面后需重新连接。</p>
          </section>

          <nav className="sidebar-card nav-card" aria-label="后台视图">
            <p className="eyebrow">视图</p>
            <div className="nav-list">
              {VIEWS.map((item) => {
                const count = navCounts[item.id];
                return (
                  <button
                    className={`nav-item${view === item.id ? " is-active" : ""}`}
                    type="button"
                    disabled={!connected}
                    aria-current={view === item.id ? "page" : undefined}
                    onClick={() => selectView(item.id)}
                    key={item.id}
                  >
                    <span className="nav-icon" aria-hidden="true">{item.shortLabel}</span>
                    <span className="nav-copy">
                      <strong>{item.label}</strong>
                      <small>{item.description}</small>
                    </span>
                    {count !== undefined ? <span className="nav-count">{count}</span> : null}
                  </button>
                );
              })}
            </div>
          </nav>

          <div className="sidebar-footnote">
            <span aria-hidden="true">↺</span>
            <p>修改笔记和表情包后会重新索引，旧向量不会继续用于检索。</p>
          </div>
        </aside>

        <main className="main-content">
          <div className="content-context">
            <span>{connected ? `当前视图 · ${currentView.label}` : "等待连接"}</span>
            {connected && view !== "stickers" && selectedGroup !== null ? <span>群 {selectedGroup}</span> : null}
          </div>

          {notice ? <NoticeBanner notice={notice} onDismiss={() => setNotice(null)} /> : null}
          {connecting && !connected ? <LoadingRows label="正在建立后台会话" /> : null}
          {!connecting || connected ? renderCurrentView() : null}
        </main>
      </div>
    </div>
  );
}

export default App;
