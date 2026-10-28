import { EmptyState, LoadingRows } from "../components/EmptyState";
import { StateBadge } from "../components/StateBadge";
import { ViewHeader } from "../components/ViewHeader";
import type { ReplyAttempt } from "../types";
import { formatDate } from "../utils/format";

interface AttemptsViewProps {
  attempts: ReplyAttempt[];
  loading: boolean;
}

function AttemptCard({ attempt }: { attempt: ReplyAttempt }) {
  const summary = attempt.reply.reason || attempt.error || "没有补充说明";
  const trace = {
    reply: attempt.reply,
    retrieval: attempt.retrieval,
    usage: attempt.usage,
    error: attempt.error,
  };

  return (
    <details className="attempt-card">
      <summary>
        <span className="attempt-summary-main">
          <time dateTime={attempt.at}>{formatDate(attempt.at)}</time>
          <StateBadge value={attempt.state} />
          <strong>{attempt.reply.action || "unknown"}</strong>
        </span>
        <span className="attempt-summary-reason">{summary}</span>
      </summary>
      <div className="attempt-details">
        <div className="metric-grid">
          <div><span>输入序号</span><strong>{attempt.input_seq}</strong></div>
          <div><span>输入 Token</span><strong>{attempt.usage.input}</strong></div>
          <div><span>输出 Token</span><strong>{attempt.usage.output}</strong></div>
          <div><span>检索耗时</span><strong>{attempt.retrieval.millis} ms</strong></div>
        </div>
        {attempt.reply.text ? (
          <div className="reply-preview">
            <span>回应文本</span>
            <p>{attempt.reply.text}</p>
          </div>
        ) : null}
        <pre>{JSON.stringify(trace, null, 2)}</pre>
      </div>
    </details>
  );
}

export function AttemptsView({ attempts, loading }: AttemptsViewProps) {
  return (
    <section className="view-stack" aria-label="回应与检索记录">
      <ViewHeader
        eyebrow="决策轨迹"
        title="回应与检索记录"
        description="检查模型动作、检索片段、Token 消耗和失败原因。"
        count={attempts.length}
      />
      <div className="panel list-panel">
        {loading && attempts.length === 0 ? <LoadingRows label="正在读取回应记录" /> : null}
        {!loading && attempts.length === 0 ? (
          <EmptyState title="还没有回应记录" description="机器人决定忽略或回应后，记录会显示在这里。" />
        ) : null}
        {attempts.map((attempt) => <AttemptCard attempt={attempt} key={attempt.id} />)}
      </div>
    </section>
  );
}
