import { useEffect, useRef, useState } from "react";
import { ApiError, type AdminApi } from "../api";
import { Button, Card, Col, Image, Input, Row, Space, Spin, Tag, Typography } from "antd";
import { EyeOutlined, SaveOutlined, StopOutlined, UndoOutlined } from "@ant-design/icons";
import { EmptyState } from "../components/EmptyState";
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

function StickerCard({ api, sticker, onReload, onError, onSuccess }: Omit<StickersViewProps, "stickers" | "loading"> & { sticker: Sticker }) {
  const [description, setDescription] = useState(sticker.description);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [previewUrl, setPreviewUrl] = useState("");
  const [previewError, setPreviewError] = useState("");
  const [validation, setValidation] = useState("");
  const previewRef = useRef<string | null>(null);

  useEffect(() => {
    if (!dirty) setDescription(sticker.description);
  }, [dirty, sticker.description]);

  useEffect(() => () => {
    if (previewRef.current) URL.revokeObjectURL(previewRef.current);
  }, []);

  async function preview() {
    setPreviewing(true);
    setPreviewError("");
    try {
      const blob = await api.getStickerImage(sticker.id);
      const nextUrl = URL.createObjectURL(blob);
      if (previewRef.current) URL.revokeObjectURL(previewRef.current);
      previewRef.current = nextUrl;
      setPreviewUrl(nextUrl);
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) setPreviewError("表情包文件不可用。");
      else onError(error, "读取表情包图片失败，请稍后重试。");
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
      await api.saveSticker(sticker.id, { version: sticker.version, description, blocked });
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
    <Card size="small" className="sticker-card" cover={<div className="sticker-preview">{previewUrl ? <Image src={previewUrl} alt={sticker.description || "表情包预览"} preview /> : <Typography.Text type="secondary">暂无预览</Typography.Text>}</div>}>
      <Space direction="vertical" size={10} style={{ width: "100%" }}>
        <Space style={{ width: "100%", justifyContent: "space-between" }}><Space wrap><StateBadge value={sticker.state} /><Tag>版本 {sticker.version}</Tag></Space>{sticker.state === "ready" ? <Button type="link" size="small" icon={<EyeOutlined />} loading={previewing} onClick={() => void preview()}>{previewUrl ? "重新预览" : "预览"}</Button> : null}</Space>
        {previewError ? <Typography.Text type="danger">{previewError}</Typography.Text> : null}
        <Input.TextArea value={description} maxLength={400} showCount status={validation ? "error" : undefined} autoSize={{ minRows: 3, maxRows: 6 }} onChange={(event) => { setDescription(event.target.value); setDirty(true); setValidation(""); }} placeholder="描述画面、情绪和适用语境" />
        <Space wrap>{sticker.tags.map((tag) => <Tag key={tag}>{tag}</Tag>)}</Space>
        <Space style={{ width: "100%", justifyContent: "space-between" }} wrap><Space direction="vertical" size={0}>{validation ? <Typography.Text type="danger">{validation}</Typography.Text> : <Typography.Text type="secondary">{runeLength(description)} / 400</Typography.Text>}</Space><Space><Button danger={sticker.state !== "blocked"} icon={sticker.state === "blocked" ? <UndoOutlined /> : <StopOutlined />} disabled={saving} onClick={() => void save(sticker.state !== "blocked")}>{sticker.state === "blocked" ? "恢复" : "禁用"}</Button><Button type="primary" icon={<SaveOutlined />} loading={saving} disabled={!dirty} onClick={() => void save(false)}>保存描述</Button></Space></Space>
      </Space>
    </Card>
  );
}

export function StickersView({ api, stickers, loading, onReload, onError, onSuccess }: StickersViewProps) {
  return (
    <section className="view-stack" aria-label="全局表情包">
      <ViewHeader eyebrow="全局素材" title="表情包" description="表情包跨群去重，发送冷却仍按群计算；禁用会阻止相同内容再次自动收入。" count={stickers.length} />
      <Card className="sticker-panel"><Spin spinning={loading} tip="正在读取表情包">{stickers.length === 0 && !loading ? <EmptyState title="还没有表情包" description="群内出现的图片经过判断和整理后，会加入全局素材库。" /> : <Row gutter={[12, 12]}>{stickers.map((sticker) => <Col xs={24} sm={12} lg={8} xl={6} key={sticker.id}><StickerCard api={api} sticker={sticker} onReload={onReload} onError={onError} onSuccess={onSuccess} /></Col>)}</Row>}</Spin></Card>
    </section>
  );
}
