import { useCallback, useEffect, useRef, useState } from "react";
import {
  App as AntdApp,
  Badge,
  Button,
  Card,
  ConfigProvider,
  Form,
  Input,
  Layout,
  Menu,
  Row,
  Col,
  Select,
  Space,
  Statistic,
  Tag,
  Typography,
} from "antd";
import {
  DatabaseOutlined,
  FileTextOutlined,
  LinkOutlined,
  MessageOutlined,
  PictureOutlined,
  ReloadOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import { AdminApi } from "./api";
import { EmptyState, LoadingRows } from "./components/EmptyState";
import { NoticeBanner } from "./components/NoticeBanner";
import type { AdminNotice, AdminView, Conversation, MemoryStatus, Sticker } from "./types";
import { noticeForError } from "./utils/errors";
import { AttemptsView } from "./views/AttemptsView";
import { ConversationView } from "./views/ConversationView";
import { MemoryView } from "./views/MemoryView";
import { NotesView } from "./views/NotesView";
import { StickersView } from "./views/StickersView";

type LoadState = "idle" | "loading" | "refreshing" | "ready" | "error";

interface ViewMeta {
  id: AdminView;
  label: string;
  description: string;
  icon: React.ReactNode;
}

const VIEWS: ViewMeta[] = [
  { id: "conversation", label: "最近对话", description: "消息与上下文", icon: <MessageOutlined /> },
  { id: "notes", label: "相处笔记", description: "长期记忆", icon: <FileTextOutlined /> },
  { id: "attempts", label: "回应记录", description: "检索与决策", icon: <ThunderboltOutlined /> },
  { id: "memory", label: "记忆状态", description: "学习与健康", icon: <DatabaseOutlined /> },
  { id: "stickers", label: "全局表情包", description: "跨群素材库", icon: <PictureOutlined /> },
];

const GROUP_VIEWS: AdminView[] = ["conversation", "notes", "attempts"];

function SummaryCard({ label, value, hint, color }: { label: string; value: string | number; hint: string; color: string }) {
  return (
    <Card className="summary-card" size="small">
      <Statistic title={label} value={value} valueStyle={{ color, fontSize: 24, fontWeight: 650 }} />
      <Typography.Text type="secondary" className="summary-hint">{hint}</Typography.Text>
    </Card>
  );
}

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
  const [memory, setMemory] = useState<MemoryStatus | null>(null);
  const [memoryState, setMemoryState] = useState<LoadState>("idle");
  const [notice, setNotice] = useState<AdminNotice | null>(null);

  const conversationRequest = useRef<AbortController | null>(null);
  const stickersRequest = useRef<AbortController | null>(null);
  const memoryRequest = useRef<AbortController | null>(null);

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
      if (controller.signal.aborted) return;
      setConversation(data);
      setConversationState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
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
      if (controller.signal.aborted) return;
      setStickers(data);
      setStickersState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
      setStickersState("error");
      showError(error, "读取表情包失败，请稍后重试。");
    }
  }, [showError]);

  const loadMemory = useCallback(async (client: AdminApi, preserve = false) => {
    memoryRequest.current?.abort();
    const controller = new AbortController();
    memoryRequest.current = controller;
    if (!preserve) {
      setMemory(null);
      setMemoryState("loading");
    } else {
      setMemoryState("refreshing");
    }
    try {
      const data = await client.getMemory(controller.signal);
      if (controller.signal.aborted) return;
      setMemory(data);
      setMemoryState("ready");
    } catch (error) {
      if (controller.signal.aborted) return;
      setMemoryState("error");
      showError(error, "读取记忆健康状态失败，请稍后重试。");
    }
  }, [showError]);

  useEffect(() => () => {
    conversationRequest.current?.abort();
    stickersRequest.current?.abort();
    memoryRequest.current?.abort();
  }, []);

  async function handleConnect() {
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
      if (nextGroup !== null) tasks.push(loadConversation(nextApi, nextGroup));
      else setConversationState("ready");
      if (view === "stickers") tasks.push(loadStickers(nextApi));
      else if (view === "memory") tasks.push(loadMemory(nextApi));
      setNotice({ tone: "success", message: nextGroups.length === 0 ? "已连接，暂无可管理的群记录。" : "后台已连接。" });
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
    memoryRequest.current?.abort();
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
    setMemory(null);
    setMemoryState("idle");
    setNotice({ tone: "info", message: "已断开后台连接，访问令牌已从当前页面清除。" });
  }

  function handleGroupChange(value: number) {
    if (!Number.isSafeInteger(value) || value <= 0) return;
    setSelectedGroup(value);
    setConversation(null);
    if (connected) void loadConversation(api, value);
  }

  function selectView(nextView: AdminView) {
    setView(nextView);
    if (nextView === "stickers" && connected && (stickersState === "idle" || stickersState === "error") && stickers.length === 0) {
      void loadStickers(api);
    } else if (nextView === "memory" && connected && (memoryState === "idle" || memoryState === "error") && memory === null) {
      void loadMemory(api);
    }
  }

  async function refreshCurrentView() {
    if (!connected) return;
    if (view === "stickers") await loadStickers(api, true);
    else if (view === "memory") await loadMemory(api, true);
    else if (selectedGroup !== null) await loadConversation(api, selectedGroup, true);
  }

  const refreshing = [conversationState, stickersState, memoryState].some((state) => state === "loading" || state === "refreshing");
  const navCounts: Record<AdminView, number | undefined> = {
    conversation: conversationGroup === selectedGroup && conversation ? conversation.messages.length : undefined,
    notes: conversationGroup === selectedGroup && conversation ? conversation.notes.length : undefined,
    attempts: conversationGroup === selectedGroup && conversation ? conversation.attempts.length : undefined,
    memory: memory ? memory.groups.length : undefined,
    stickers: stickersState === "ready" || stickers.length > 0 ? stickers.length : undefined,
  };

  function renderCurrentView() {
    if (!connected) {
      return <Card className="welcome-panel"><Typography.Title level={2}>连接后台后开始查看</Typography.Title><Typography.Paragraph>输入访问令牌；未配置令牌的本机服务可以直接连接。令牌只保存在当前页面内存中。</Typography.Paragraph></Card>;
    }
    if (GROUP_VIEWS.includes(view) && selectedGroup === null) {
      return <EmptyState title="暂无可查看的群" description="数据库中还没有群记录。机器人接入群并产生消息后，这里会出现可选群。" />;
    }
    const loadingConversation = conversationState === "loading" || conversationState === "refreshing";
    if (view === "conversation") return <ConversationView messages={conversation?.messages ?? []} loading={loadingConversation} />;
    if (view === "notes" && selectedGroup !== null) return <NotesView api={api} group={selectedGroup} notes={conversation?.notes ?? []} loading={loadingConversation} onReload={() => loadConversation(api, selectedGroup, true)} onError={showError} onSuccess={showSuccess} />;
    if (view === "attempts") return <AttemptsView attempts={conversation?.attempts ?? []} loading={loadingConversation} />;
    if (view === "memory") return <MemoryView status={memory} loading={memoryState === "loading" || memoryState === "refreshing"} onReload={() => loadMemory(api, true)} />;
    return <StickersView api={api} stickers={stickers} loading={stickersState === "loading" || stickersState === "refreshing"} onReload={() => loadStickers(api, true)} onError={showError} onSuccess={showSuccess} />;
  }

  const currentView = VIEWS.find((item) => item.id === view) ?? VIEWS[0];
  const menuItems = VIEWS.map((item) => ({
    key: item.id,
    icon: item.icon,
    disabled: !connected,
    label: <Space className="menu-label" size={8}><Space direction="vertical" size={0}><Typography.Text>{item.label}</Typography.Text><Typography.Text type="secondary" className="menu-description">{item.description}</Typography.Text></Space>{navCounts[item.id] !== undefined ? <Badge count={navCounts[item.id]} showZero size="small" /> : null}</Space>,
  }));

  return (
    <ConfigProvider theme={{ token: { colorPrimary: "#1677ff", borderRadius: 8, colorBgLayout: "#f5f7fa" } }}>
      <AntdApp>
        <Layout className="admin-app">
          <Layout.Header className="admin-header">
            <Space className="admin-brand" size={12} align="center">
              <div className="brand-mark">群</div>
              <div><Typography.Text type="secondary" className="brand-eyebrow">GO AGENT ADMIN</Typography.Text><Typography.Title level={3}>群友 · 运行与记忆</Typography.Title><Typography.Text type="secondary">查看真实对话、检索结果和长期记忆</Typography.Text></div>
            </Space>
            <Space className="header-actions">
              <Tag color={connecting ? "processing" : connected ? "success" : "default"} icon={<LinkOutlined />}>{connecting ? "连接中" : connected ? "已连接" : "未连接"}</Tag>
              <Button icon={<ReloadOutlined />} loading={refreshing} disabled={!connected || (GROUP_VIEWS.includes(view) && selectedGroup === null)} onClick={() => void refreshCurrentView()}>刷新数据</Button>
            </Space>
          </Layout.Header>
          <Layout className="admin-layout">
            <Layout.Sider width={248} theme="light" className="admin-sidebar">
              <Card size="small" title="服务访问" extra={connected ? <Button type="link" danger size="small" onClick={disconnect}>断开</Button> : null}>
                <Form layout="vertical" onFinish={() => void handleConnect()}>
                  <Form.Item label="访问令牌"><Input.Password value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" spellCheck={false} placeholder="本机未配置时可留空" /></Form.Item>
                  <Button type="primary" htmlType="submit" block loading={connecting}>{connected ? "重新连接" : "连接后台"}</Button>
                </Form>
                <Form layout="vertical" className="group-form">
                  <Form.Item label="当前群"><Select value={selectedGroup ?? undefined} disabled={!connected || groups.length === 0} placeholder="暂无群记录" options={groups.map((group) => ({ value: group, label: `群 ${group}` }))} onChange={handleGroupChange} /></Form.Item>
                </Form>
                <Typography.Text type="secondary" className="privacy-note">令牌不会写入本地存储；刷新页面后需重新连接。</Typography.Text>
              </Card>
              <Card size="small" title="视图" className="nav-card"><Menu mode="inline" selectedKeys={[view]} items={menuItems} onClick={({ key }) => selectView(key as AdminView)} /></Card>
              <Typography.Text type="secondary" className="sidebar-footnote">修改笔记和表情包后会重新索引，旧向量不会继续用于检索。</Typography.Text>
            </Layout.Sider>
            <Layout.Content className="admin-content">
                <Space className="content-context" split="·"><Typography.Text type="secondary">{connected ? `当前视图 · ${currentView.label}` : "等待连接"}</Typography.Text>{connected && GROUP_VIEWS.includes(view) && selectedGroup !== null ? <Typography.Text type="secondary">群 {selectedGroup}</Typography.Text> : null}</Space>
                {notice ? <NoticeBanner notice={notice} onDismiss={() => setNotice(null)} /> : null}
                {connected ? <Row gutter={[12, 12]} className="summary-grid"><Col xs={12} md={6}><SummaryCard label="当前群" value={selectedGroup === null ? "—" : selectedGroup} hint={selectedGroup === null ? "暂无群记录" : "正在查看的群"} color="#1677ff" /></Col><Col xs={12} md={6}><SummaryCard label="最近消息" value={navCounts.conversation ?? 0} hint="当前上下文窗口" color="#13c2c2" /></Col><Col xs={12} md={6}><SummaryCard label="有效笔记" value={navCounts.notes ?? 0} hint="可继续参与召回" color="#52c41a" /></Col><Col xs={12} md={6}><SummaryCard label="回应记录" value={navCounts.attempts ?? 0} hint="模型决策与检索轨迹" color="#722ed1" /></Col></Row> : null}
                {connecting && !connected ? <LoadingRows label="正在建立后台会话" /> : null}
                {!connecting || connected ? renderCurrentView() : null}
            </Layout.Content>
          </Layout>
        </Layout>
      </AntdApp>
    </ConfigProvider>
  );
}

export default App;
