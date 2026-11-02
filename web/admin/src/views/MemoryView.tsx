import { Button, Card, Col, Empty, List, Row, Space, Spin, Statistic, Tag, Typography } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import { EmptyState } from "../components/EmptyState";
import { ViewHeader } from "../components/ViewHeader";
import type { MemoryStatus } from "../types";

interface MemoryViewProps {
  status: MemoryStatus | null;
  loading: boolean;
  onReload: () => Promise<void>;
}

function percent(value: number) {
  return `${Math.round(value * 100)}%`;
}

function MetricCard({ label, value }: { label: string; value: string | number }) {
  return <Card size="small" className="metric-card"><Statistic title={label} value={value} valueStyle={{ fontSize: 18 }} /></Card>;
}

function Section({ title, description, children }: { title: string; description: string; children: React.ReactNode }) {
  return <Card title={title} size="small" extra={<Typography.Text type="secondary">指标</Typography.Text>}><Typography.Paragraph type="secondary" className="section-description">{description}</Typography.Paragraph><Row gutter={[8, 8]}>{children}</Row></Card>;
}

export function MemoryView({ status, loading, onReload }: MemoryViewProps) {
  if (loading && !status) return <LoadingMemory />;
  if (!status) return <EmptyState title="暂无可读取的记忆状态" description="连接后台并产生消息后，这里会显示学习、召回和账本指标。" />;

  const totalLag = status.groups.reduce((sum, group) => sum + group.lag, 0);
  const maxLag = status.groups.reduce((max, group) => Math.max(max, group.lag), 0);
  const lagGroups = status.groups.filter((group) => group.lag > 0).slice(0, 8);

  return (
    <section className="view-stack" aria-label="记忆健康状态">
      <ViewHeader eyebrow="长期记忆" title="记忆健康" description={`最近 ${status.window_hours} 小时的学习、召回与记账状态。指标用于发现积压和失效，不直接代表模型回答质量。`} count={status.groups.length} action={<Button icon={<ReloadOutlined />} onClick={() => void onReload()} loading={loading}>刷新指标</Button>} />
      <Card className="memory-summary"><Row gutter={[12, 12]}>{[["学习总积压", totalLag], ["最大群积压", maxLag], ["召回命中率", percent(status.recall.hit_rate)], ["待建索引", status.pending_embeddings]].map(([label, value]) => <Col xs={12} md={6} key={String(label)}><Statistic title={label} value={value} /></Col>)}</Row></Card>
      <Row gutter={[12, 12]}>
        <Col xs={24} md={12}><Section title="学习" description="候选抽取与旧笔记协调；拒绝数包含被丢弃或主动拒绝的候选。"><Col span={12}><MetricCard label="运行" value={status.learning.runs} /></Col><Col span={12}><MetricCard label="成功 / 失败" value={`${status.learning.succeeded} / ${status.learning.failed}`} /></Col><Col span={12}><MetricCard label="拒绝候选" value={status.learning.rejected_count} /></Col><Col span={12}><MetricCard label="产生变更" value={status.learning.change_count} /></Col><Col span={12}><MetricCard label="平均耗时" value={`${Math.round(status.learning.avg_duration_ms)} ms`} /></Col><Col span={12}><MetricCard label="抽取 / 协调" value={`${Math.round(status.learning.avg_extract_ms)} / ${Math.round(status.learning.avg_reconcile_ms)} ms`} /></Col></Section></Col>
        <Col xs={24} md={12}><Section title="召回" description="查看最近窗口内的检索命中、缓存和语义降级情况。"><Col span={12}><MetricCard label="召回请求" value={status.recall.attempted} /></Col><Col span={12}><MetricCard label="命中 / 空召回" value={`${status.recall.hits} / ${status.recall.empty}`} /></Col><Col span={12}><MetricCard label="缓存命中率" value={percent(status.recall.cache_hit_rate)} /></Col><Col span={12}><MetricCard label="语义降级" value={status.recall.semantic_degraded} /></Col><Col span={12}><MetricCard label="最大索引加载" value={`${status.recall.max_index_load_ms} ms`} /></Col><Col span={12}><MetricCard label="空召回率" value={percent(status.recall.empty_rate)} /></Col></Section></Col>
        <Col xs={24} md={12}><Section title="使用账本" description="只有已确认送达的回复才应增加账本和投影。"><Col span={12}><MetricCard label="确认回复" value={status.ledger.confirmed_attempts} /></Col><Col span={12}><MetricCard label="账本记录" value={status.ledger.usage_rows} /></Col><Col span={12}><MetricCard label="写入失败" value={status.ledger.write_failures} /></Col><Col span={12}><MetricCard label="账本与投影差异" value={status.usage_projection_diff} /></Col></Section></Col>
        <Col xs={24} md={12}><Section title="笔记" description="检查 active 规模、身份唯一索引和遗忘保护。"><Col span={12}><MetricCard label="active / 总量" value={`${status.notes.active_notes} / ${status.notes.total_notes}`} /></Col><Col span={12}><MetricCard label="active 占比" value={percent(status.notes.active_ratio)} /></Col><Col span={12}><MetricCard label="重复身份率" value={percent(status.notes.duplicate_rate)} /></Col><Col span={12}><MetricCard label="active 唯一索引" value={status.notes.active_identity_index ? "正常" : "待清理"} /></Col><Col span={12}><MetricCard label="遗忘重建拦截" value={status.notes.forgotten_rebuilds} /></Col><Col span={12}><MetricCard label="重复身份数" value={status.notes.duplicate_identities} /></Col></Section></Col>
      </Row>
      <Card title="学习积压" size="small"><Typography.Paragraph type="secondary">lag 是最新消息序号与该群学习游标的差。没有积压的群不列出。</Typography.Paragraph>{lagGroups.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前没有学习积压" /> : <List grid={{ gutter: 8, xs: 1, sm: 2, md: 4 }} dataSource={lagGroups} renderItem={(group) => <List.Item><Card size="small"><Space style={{ width: "100%", justifyContent: "space-between" }}><Typography.Text>群 {group.group_id}</Typography.Text><Tag color="warning">{group.lag}</Tag></Space></Card></List.Item>} />}</Card>
    </section>
  );
}

function LoadingMemory() {
  return <Card><Spin spinning tip="正在读取记忆健康状态"><div className="loading-placeholder" /></Spin></Card>;
}
