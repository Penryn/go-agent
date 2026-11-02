import { Card, Collapse, Descriptions, Spin, Typography } from "antd";
import { EmptyState } from "../components/EmptyState";
import { StateBadge } from "../components/StateBadge";
import { ViewHeader } from "../components/ViewHeader";
import type { ReplyAttempt } from "../types";
import { formatDate } from "../utils/format";

interface AttemptsViewProps {
  attempts: ReplyAttempt[];
  loading: boolean;
}

function attemptPanel(attempt: ReplyAttempt) {
  const summary = attempt.reply.reason || attempt.error || "没有补充说明";
  const trace = { reply: attempt.reply, retrieval: attempt.retrieval, usage: attempt.usage, error: attempt.error };
  return {
    key: attempt.id,
    label: <span className="attempt-label"><Typography.Text type="secondary">{formatDate(attempt.at)}</Typography.Text><StateBadge value={attempt.state} /><Typography.Text strong>{attempt.reply.action || "unknown"}</Typography.Text></span>,
    extra: <Typography.Text type="secondary" ellipsis={{ tooltip: summary }}>{summary}</Typography.Text>,
    children: (
      <div className="attempt-details">
        <Descriptions size="small" bordered column={{ xs: 1, sm: 2, md: 4 }} items={[
          { key: "seq", label: "输入序号", children: attempt.input_seq },
          { key: "input", label: "输入 Token", children: attempt.usage.input },
          { key: "output", label: "输出 Token", children: attempt.usage.output },
          { key: "retrieval", label: "检索耗时", children: `${attempt.retrieval.millis} ms` },
        ]} />
        {attempt.reply.text ? <Card size="small" type="inner" title="回应文本"><Typography.Paragraph className="reply-preview-text">{attempt.reply.text}</Typography.Paragraph></Card> : null}
        <Typography.Text type="secondary">完整追踪</Typography.Text>
        <pre>{JSON.stringify(trace, null, 2)}</pre>
      </div>
    ),
  };
}

export function AttemptsView({ attempts, loading }: AttemptsViewProps) {
  return (
    <section className="view-stack" aria-label="回应与检索记录">
      <ViewHeader eyebrow="决策轨迹" title="回应与检索记录" description="检查模型动作、检索片段、Token 消耗和失败原因。" count={attempts.length} />
      <Card className="list-panel" bodyStyle={{ padding: "0 16px" }}>
        <Spin spinning={loading} tip="正在读取回应记录">
          {attempts.length === 0 && !loading ? <EmptyState title="还没有回应记录" description="机器人决定忽略或回应后，记录会显示在这里。" /> : <Collapse items={attempts.map(attemptPanel)} />}
        </Spin>
      </Card>
    </section>
  );
}
