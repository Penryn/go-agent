import { useEffect, useId, useRef, useState } from "react";
import { ApiError, type AdminApi } from "../api";
import { EmptyState, LoadingRows } from "../components/EmptyState";
import { StateBadge } from "../components/StateBadge";
import { ViewHeader } from "../components/ViewHeader";
import type { Sticker } from "../types";
import { runeLength } from "../utils/format";

interface StickersViewProps {
  api: AdminApi;
  stickers: Sticker[];
  loading: boolean;
  onReload: () => Promise<void>;
  onError: (error: unknown, fallback: string) => void;
  onSuccess: (message: string) => void;
}

interface StickerCardProps extends Omit<StickersViewProps, "stickers" | "loading"> {
  sticker: Sticker;
}

function StickerCard({ api, sticker, onReload, onError, onSuccess }: StickerCardProps) {
  const [description, setDescription] = useState(sticker.description);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [previewUrl, setPreviewUrl] = useState("");
  const [previewError, setPreviewError] = useState("");
  const [validation, setValidation] = useState("");
  const previewRef = useRef<string | null>(null);
  const errorId = useId();

  useEffect(() => {
    if (!dirty) {
      setDescription(sticker.description);
    }
  }, [dirty, sticker.description]);

  useEffect(() => () => {
    if (previewRef.current) {
      URL.revokeObjectURL(previewRef.current);
    }
  }, []);

  async function preview() {
    setPreviewing(true);
    setPreviewError("");
    try {
      const blob = await api.getStickerImage(sticker.id);
      const nextUrl = URL.createObjectURL(blob);
      if (previewRef.current) {
        URL.revokeObjectURL(previewRef.current);
      }
      previewRef.current = nextUrl;
      setPreviewUrl(nextUrl);
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) {
        setPreviewError("表情包文件不可用。");
      } else {
        onError(error, "读取表情包图片失败，请稍后重试。");
      }
    } finally {
      setPreviewing(false);
    }
  }

  async function save(blocked: boolean) {
    if (!blocked && !description) {
      setValidation("恢复表情包前需要填写含义。");
      return;
    }
    if (runeLength(description) > 400) {
      setValidation("表情包含义不能超过 400 个字符。");
      return;
    }

    setValidation("");
    setSaving(true);
    try {
      await api.saveSticker(sticker.id, {
        version: sticker.version,
        description,
        blocked,
      });
      setDirty(false);
      onSuccess(blocked ? "表情包已禁用。" : sticker.state === "blocked" ? "表情包已恢复。" : "表情包描述已保存。");
      await onReload();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        await onReload();
        onError(error, "表情包已更新，但保存失败。草稿仍保留在当前页面。");
      } else {
        onError(error, "保存表情包失败，请稍后重试。");
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className="sticker-card">
      <div className="sticker-preview">
        {previewUrl ? <img src={previewUrl} alt={sticker.description || "表情包预览"} /> : (
          <div className="sticker-placeholder" aria-hidden="true">表</div>
        )}
      </div>
      <div className="item-heading">
        <div className="item-kicker">
          <StateBadge value={sticker.state} />
          <span>版本 {sticker.version}</span>
        </div>
        {sticker.state === "ready" ? (
          <button className="button button-quiet button-small" type="button" disabled={previewing} onClick={() => void preview()}>
            {previewing ? "读取中…" : previewUrl ? "重新预览" : "预览"}
          </button>
        ) : null}
      </div>
      {previewError ? <p className="inline-error">{previewError}</p> : null}
      <label className="sr-only" htmlFor={`${errorId}-sticker`}>表情包含义</label>
      <textarea
        id={`${errorId}-sticker`}
        value={description}
        maxLength={400}
        aria-describedby={validation ? errorId : undefined}
        aria-invalid={validation ? true : undefined}
        onChange={(event) => {
          setDescription(event.target.value);
          setDirty(true);
          setValidation("");
        }}
        placeholder="描述画面、情绪和适用语境"
      />
      <div className="tag-list">
        {sticker.tags.map((tag) => <span className="tag" key={tag}>{tag}</span>)}
      </div>
      <div className="editor-footer sticker-footer">
        <span className="field-hint">{runeLength(description)} / 400</span>
        {validation ? <span className="field-error" id={errorId}>{validation}</span> : null}
        <div className="button-row">
          <button className="button button-secondary" type="button" disabled={saving} onClick={() => void save(sticker.state !== "blocked")}>
            {sticker.state === "blocked" ? "恢复" : "禁用"}
          </button>
          <button className="button button-primary" type="button" disabled={saving || !dirty} onClick={() => void save(false)}>
            {saving ? "保存中…" : "保存描述"}
          </button>
        </div>
      </div>
    </article>
  );
}

export function StickersView({ api, stickers, loading, onReload, onError, onSuccess }: StickersViewProps) {
  return (
    <section className="view-stack" aria-label="全局表情包">
      <ViewHeader
        eyebrow="全局素材"
        title="表情包"
        description="表情包跨群去重，发送冷却仍按群计算；禁用会阻止相同内容再次自动收入。"
        count={stickers.length}
      />
      <div className="panel">
        {loading && stickers.length === 0 ? <LoadingRows label="正在读取表情包" /> : null}
        {!loading && stickers.length === 0 ? (
          <EmptyState title="还没有表情包" description="群内出现的图片经过判断和整理后，会加入全局素材库。" />
        ) : null}
        <div className="sticker-grid">
          {stickers.map((sticker) => (
            <StickerCard
              api={api}
              sticker={sticker}
              onReload={onReload}
              onError={onError}
              onSuccess={onSuccess}
              key={sticker.id}
            />
          ))}
        </div>
      </div>
    </section>
  );
}
