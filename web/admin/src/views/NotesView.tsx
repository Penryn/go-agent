import { useEffect, useId, useState } from "react";
import { ApiError, type AdminApi } from "../api";
import { EmptyState, LoadingRows } from "../components/EmptyState";
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

interface NoteEditorProps extends Omit<NotesViewProps, "notes" | "loading"> {
  note: Note;
}

function NoteEditor({ api, group, note, onReload, onError, onSuccess }: NoteEditorProps) {
  const [text, setText] = useState(note.text);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [validation, setValidation] = useState("");
  const errorId = useId();

  useEffect(() => {
    if (!dirty) {
      setText(note.text);
    }
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
      await api.saveNote(note.id, {
        group,
        version: note.version,
        text: nextText,
        forget,
      });
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
    <article className="note-card">
      <div className="item-heading">
        <div>
          <div className="item-kicker">
            <StateBadge value={note.state} />
            <span>{note.kind}</span>
            <span>{note.subject ? `用户 ${note.subject}` : "本群"}</span>
          </div>
          <h3>{note.key}</h3>
        </div>
        <time dateTime={note.updated}>{formatDate(note.updated)}</time>
      </div>

      <label className="sr-only" htmlFor={`${errorId}-note`}>笔记内容</label>
      <textarea
        id={`${errorId}-note`}
        value={text}
        maxLength={600}
        aria-describedby={validation ? errorId : undefined}
        aria-invalid={validation ? true : undefined}
        onChange={(event) => {
          setText(event.target.value);
          setDirty(true);
          setValidation("");
        }}
        placeholder="写下需要保留的群内事实或相处偏好"
      />
      <div className="editor-footer">
        <span className="field-hint">{runeLength(text)} / 600</span>
        {validation ? <span className="field-error" id={errorId}>{validation}</span> : null}
        <div className="button-row">
          <button className="button button-secondary" type="button" disabled={saving || note.state === "forgotten"} onClick={() => void save(true)}>
            遗忘
          </button>
          <button className="button button-primary" type="button" disabled={saving || !dirty} onClick={() => void save(false)}>
            {saving ? "保存中…" : "保存修改"}
          </button>
        </div>
      </div>

      {note.evidence.length > 0 ? (
        <details className="evidence-list">
          <summary>查看 {note.evidence.length} 条原始依据</summary>
          {note.evidence.map((evidence, index) => (
            <blockquote key={`${evidence.event_id}-${index}`}>
              <p>{evidence.quote}</p>
              <cite>{evidence.event_id}</cite>
            </blockquote>
          ))}
        </details>
      ) : null}
    </article>
  );
}

export function NotesView({ api, group, notes, loading, onReload, onError, onSuccess }: NotesViewProps) {
  return (
    <section className="view-stack" aria-label="相处笔记">
      <ViewHeader
        eyebrow="长期记忆"
        title="相处笔记"
        description="修改后会触发重新索引；遗忘的笔记不会继续进入检索上下文。"
        count={notes.length}
      />
      <div className="panel list-panel">
        {loading && notes.length === 0 ? <LoadingRows label="正在读取群笔记" /> : null}
        {!loading && notes.length === 0 ? (
          <EmptyState title="还没有笔记" description="群聊中出现稳定事实或相处偏好后，笔记会显示在这里。" />
        ) : null}
        {notes.map((note) => (
          <NoteEditor
            api={api}
            group={group}
            note={note}
            onReload={onReload}
            onError={onError}
            onSuccess={onSuccess}
            key={`${group}:${note.id}`}
          />
        ))}
      </div>
    </section>
  );
}
