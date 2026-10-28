import { EmptyState, LoadingRows } from "../components/EmptyState";
import { StateBadge } from "../components/StateBadge";
import { ViewHeader } from "../components/ViewHeader";
import type { Message } from "../types";
import { formatDate } from "../utils/format";

interface ConversationViewProps {
  messages: Message[];
  loading: boolean;
}

function attachmentSummary(message: Message): string[] {
  const images = message.images ?? [];
  const stickers = images.filter((image) => image.sticker).length;
  const pictures = images.length - stickers;
  const summary: string[] = [];
  if (stickers > 0) {
    summary.push(`表情 ${stickers}`);
  }
  if (pictures > 0) {
    summary.push(`图片 ${pictures}`);
  }
  return summary;
}

function MessageCard({ message }: { message: Message }) {
  const author = message.name || `用户 ${message.user_id}`;
  const attachments = attachmentSummary(message);

  return (
    <article className={`message-card${message.recalled ? " is-recalled" : ""}`}>
      <div className="message-meta">
        <StateBadge value={message.outbound ? "sent" : "received"} label={message.outbound ? "机器人" : "群友"} />
        <strong>{author}</strong>
        <span className="user-id">#{message.user_id}</span>
        <time dateTime={message.at}>{formatDate(message.at)}</time>
        {message.directed ? <span className="tag tag-accent">点名</span> : null}
        {message.recalled ? <span className="tag tag-danger">已撤回</span> : null}
      </div>
      <p className="message-text">
        {message.text || (attachments.length > 0 ? "（图片消息）" : "（无文本内容）")}
      </p>
      {(attachments.length > 0 || message.reply_to) ? (
        <div className="message-tags">
          {attachments.map((item) => <span className="tag" key={item}>{item}</span>)}
          {message.reply_to ? <span className="tag">回复消息</span> : null}
        </div>
      ) : null}
    </article>
  );
}

export function ConversationView({ messages, loading }: ConversationViewProps) {
  return (
    <section className="view-stack" aria-label="最近对话">
      <ViewHeader
        eyebrow="实时上下文"
        title="最近对话"
        description="按时间倒序查看群消息、点名关系和机器人发送记录。"
        count={messages.length}
      />
      <div className="panel list-panel">
        {loading && messages.length === 0 ? <LoadingRows label="正在读取最近对话" /> : null}
        {!loading && messages.length === 0 ? (
          <EmptyState title="还没有消息" description="这个群尚未产生可查看的对话记录。" />
        ) : null}
        {messages.map((message) => <MessageCard message={message} key={message.id} />)}
      </div>
    </section>
  );
}
