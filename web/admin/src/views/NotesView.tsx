import { useEffect, useState } from "react";
import { ApiError, type AdminApi } from "../api";
import { Button, Card, Collapse, Input, Space, Spin, Tag, Typography } from "antd";
import { DeleteOutlined, SaveOutlined } from "@ant-design/icons";
import { EmptyState } from "../components/EmptyState";
import { StateBadge } from "../components/StateBadge";
import { ViewHeader } from "../components/ViewHeader";
import type { Note } from "../types";
import { formatDate, runeLength } from "../utils/format";

interface NotesViewProps {
  api: AdminApi;
  group: number;
  notes: Note[];
  loading: boolean;
  onReload: () => Promise<void>;
  onError: (error: unknown, fallback: string) => void;
  onSuccess: (message: string) => void;
}

function NoteEditor({ api, group, note, onReload, onError, onSuccess }: Omit<NotesViewProps, "notes" | "loading"> & { note: Note }) {
  const [text, setText] = useState(note.text);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [validation, setValidation] = useState("");

  useEffect(() => {
    if (!dirty) setText(note.text);
  }, [dirty, note.text]);

  async function save(forget: boolean) {
    const nextText = forget ? "" : text;
    if (!forget && !nextText) {
      setValidation("笔记内容不能为空。");
      return;
    }
    if (runeLength(nextText) > 600) {
      setValidation("笔记内容不能超过 600 个字符。");
      return;
    }
    setValidation("");
    setSaving(true);
    try {
      await api.saveNote(note.id, { group, version: note.version, text: nextText, forget });
      setDirty(false);
      onSuccess(forget ? "笔记已遗忘。" : "笔记修改已保存。");
      await onReload();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        await onReload();
        onError(error, "笔记已更新，但保存失败。草稿仍保留在当前页面。");
      } else {
        onError(error, "保存笔记失败，请稍后重试。");
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card size="small" className="note-card">
      <Space direction="vertical" size={12} style={{ width: "100%" }}>
        <Space className="item-heading" align="start" style={{ width: "100%", justifyContent: "space-between" }}>
          <div><Space wrap size={[6, 4]}><StateBadge value={note.state} /><Tag>{note.kind}</Tag><Tag>{note.subject ? `用户 ${note.subject}` : "本群"}</Tag></Space><Typography.Title level={5} className="note-title">{note.key}</Typography.Title></div>
          <Typography.Text type="secondary">{formatDate(note.updated)}</Typography.Text>
        </Space>
        <Input.TextArea value={text} maxLength={600} showCount status={validation ? "error" : undefined} autoSize={{ minRows: 4, maxRows: 10 }} onChange={(event) => { setText(event.target.value); setDirty(true); setValidation(""); }} placeholder="写下需要保留的群内事实或相处偏好" />
        <Space className="editor-footer" wrap style={{ width: "100%", justifyContent: "space-between" }}>
          <Space direction="vertical" size={0}>{validation ? <Typography.Text type="danger">{validation}</Typography.Text> : <Typography.Text type="secondary">{runeLength(text)} / 600</Typography.Text>}</Space>
          <Space><Button danger icon={<DeleteOutlined />} disabled={saving || note.state === "forgotten"} onClick={() => void save(true)}>遗忘</Button><Button type="primary" icon={<SaveOutlined />} loading={saving} disabled={!dirty} onClick={() => void save(false)}>保存修改</Button></Space>
        </Space>
        {note.evidence.length > 0 ? <Collapse ghost items={[{ key: "evidence", label: `查看 ${note.evidence.length} 条原始依据`, children: <Space direction="vertical" style={{ width: "100%" }}>{note.evidence.map((evidence, index) => <Card size="small" type="inner" key={`${evidence.event_id}-${index}`}><Typography.Paragraph>{evidence.quote}</Typography.Paragraph><Typography.Text type="secondary">{evidence.event_id}</Typography.Text></Card>)}</Space> }]} /> : null}
      </Space>
    </Card>
  );
}

export function NotesView({ api, group, notes, loading, onReload, onError, onSuccess }: NotesViewProps) {
  return (
    <section className="view-stack" aria-label="相处笔记">
      <ViewHeader eyebrow="长期记忆" title="相处笔记" description="修改后会触发重新索引；遗忘的笔记不会继续进入检索上下文。" count={notes.length} />
      <Card className="list-panel" bodyStyle={{ padding: "4px 16px" }}>
        <Spin spinning={loading} tip="正在读取群笔记">
          {notes.length === 0 && !loading ? <EmptyState title="还没有笔记" description="群聊中出现稳定事实或相处偏好后，笔记会显示在这里。" /> : notes.map((note) => <NoteEditor key={`${group}:${note.id}`} api={api} group={group} note={note} onReload={onReload} onError={onError} onSuccess={onSuccess} />)}
        </Spin>
      </Card>
    </section>
  );
}
