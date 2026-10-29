import type { ReactNode } from "react";
import { EmptyState, LoadingRows } from "../components/EmptyState";
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

function Metric({ label, value }: { label: string; value: string | number }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function Section({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <section className="memory-card">
      <div className="memory-card-heading">
        <h3>{title}</h3>
        <p>{description}</p>
      </div>
      <div className="metric-grid">{children}</div>
    </section>
  );
}

export function MemoryView({ status, loading, onReload }: MemoryViewProps) {
  if (loading && !status) {
    return <LoadingRows label="正在读取记忆健康状态" />;
  }
  if (!status) {
    return <EmptyState title="暂无可读取的记忆状态" description="连接后台并产生消息后，这里会显示学习、召回和账本指标。" />;
  }

  const totalLag = status.groups.reduce((sum, group) => sum + group.lag, 0);
  const maxLag = status.groups.reduce((max, group) => Math.max(max, group.lag), 0);
  const lagGroups = status.groups.filter((group) => group.lag > 0).slice(0, 8);

  return (
    <section className="view-stack" aria-label="记忆健康状态">
      <ViewHeader
        eyebrow="长期记忆"
        title="记忆健康"
        description={`最近 ${status.window_hours} 小时的学习、召回与记账状态。指标用于发现积压和失效，不直接代表模型回答质量。`}
        count={status.groups.length}
        action={<button className="button button-secondary" type="button" onClick={() => void onReload()}>刷新指标</button>}
      />

      <div className="memory-summary panel">
        <div className="metric-grid">
          <Metric label="学习总积压" value={totalLag} />
          <Metric label="最大群积压" value={maxLag} />
          <Metric label="召回命中率" value={percent(status.recall.hit_rate)} />
          <Metric label="待建索引" value={status.pending_embeddings} />
        </div>
      </div>

      <div className="memory-grid">
        <Section title="学习" description="候选抽取与旧笔记协调；拒绝数包含被丢弃或主动拒绝的候选。">
          <Metric label="运行" value={status.learning.runs} />
          <Metric label="成功 / 失败" value={`${status.learning.succeeded} / ${status.learning.failed}`} />
          <Metric label="拒绝候选" value={status.learning.rejected_count} />
          <Metric label="产生变更" value={status.learning.change_count} />
          <Metric label="平均耗时" value={`${Math.round(status.learning.avg_duration_ms)} ms`} />
          <Metric label="抽取 / 协调" value={`${Math.round(status.learning.avg_extract_ms)} / ${Math.round(status.learning.avg_reconcile_ms)} ms`} />
        </Section>

        <Section title="召回" description="查看最近窗口内的检索命中、缓存和语义降级情况。">
          <Metric label="召回请求" value={status.recall.attempted} />
          <Metric label="命中 / 空召回" value={`${status.recall.hits} / ${status.recall.empty}`} />
          <Metric label="缓存命中率" value={percent(status.recall.cache_hit_rate)} />
          <Metric label="语义降级" value={status.recall.semantic_degraded} />
          <Metric label="最大索引加载" value={`${status.recall.max_index_load_ms} ms`} />
          <Metric label="空召回率" value={percent(status.recall.empty_rate)} />
        </Section>

        <Section title="使用账本" description="只有已确认送达的回复才应增加账本和投影。">
          <Metric label="确认回复" value={status.ledger.confirmed_attempts} />
          <Metric label="账本记录" value={status.ledger.usage_rows} />
          <Metric label="写入失败" value={status.ledger.write_failures} />
          <Metric label="账本与投影差异" value={status.usage_projection_diff} />
        </Section>

        <Section title="笔记" description="检查 active 规模、身份唯一索引和遗忘保护。">
          <Metric label="active / 总量" value={`${status.notes.active_notes} / ${status.notes.total_notes}`} />
          <Metric label="active 占比" value={percent(status.notes.active_ratio)} />
          <Metric label="重复身份率" value={percent(status.notes.duplicate_rate)} />
          <Metric label="active 唯一索引" value={status.notes.active_identity_index ? "正常" : "待清理"} />
          <Metric label="遗忘重建拦截" value={status.notes.forgotten_rebuilds} />
          <Metric label="重复身份数" value={status.notes.duplicate_identities} />
        </Section>
      </div>

      <section className="memory-card memory-lag-card">
        <div className="memory-card-heading">
          <h3>学习积压</h3>
          <p>lag 是最新消息序号与该群学习游标的差。没有积压的群不列出。</p>
        </div>
        {lagGroups.length === 0 ? (
          <p className="memory-empty">当前没有学习积压。</p>
        ) : (
          <div className="lag-list">
            {lagGroups.map((group) => (
              <div className="lag-row" key={group.group_id}>
                <span>群 {group.group_id}</span>
                <strong>{group.lag}</strong>
              </div>
            ))}
          </div>
        )}
      </section>
    </section>
  );
}
