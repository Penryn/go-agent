import { Card, List, Space, Spin, Tag, Typography } from "antd";
import { EmptyState } from "../components/EmptyState";
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
  return [stickers > 0 ? `表情 ${stickers}` : "", pictures > 0 ? `图片 ${pictures}` : ""].filter(Boolean);
}

function MessageItem({ message }: { message: Message }) {
  const author = message.name || `用户 ${message.user_id}`;
  const attachments = attachmentSummary(message);
  return (
    <List.Item className={message.recalled ? "message-item recalled" : "message-item"}>
      <Space direction="vertical" size={8} style={{ width: "100%" }}>
        <Space wrap size={[8, 4]}>
          <StateBadge value={message.outbound ? "sent" : "received"} label={message.outbound ? "机器人" : "群友"} />
          <Typography.Text strong>{author}</Typography.Text>
          <Typography.Text type="secondary">#{message.user_id}</Typography.Text>
          <Typography.Text type="secondary">{formatDate(message.at)}</Typography.Text>
          {message.directed ? <Tag color="blue">点名</Tag> : null}
          {message.recalled ? <Tag color="error">已撤回</Tag> : null}
        </Space>
        <Typography.Paragraph className="message-text">{message.text || (attachments.length > 0 ? "（图片消息）" : "（无文本内容）")}</Typography.Paragraph>
        {attachments.length > 0 || message.reply_to ? <Space wrap size={[6, 4]}>{attachments.map((item) => <Tag key={item}>{item}</Tag>)}{message.reply_to ? <Tag>回复消息</Tag> : null}</Space> : null}
      </Space>
    </List.Item>
  );
}

export function ConversationView({ messages, loading }: ConversationViewProps) {
  return (
    <section className="view-stack" aria-label="最近对话">
      <ViewHeader eyebrow="实时上下文" title="最近对话" description="按时间倒序查看群消息、点名关系和机器人发送记录。" count={messages.length} />
      <Card className="list-panel" bodyStyle={{ padding: "0 20px" }}>
        <Spin spinning={loading} tip="正在读取最近对话">
          {messages.length === 0 && !loading ? <EmptyState title="还没有消息" description="这个群尚未产生可查看的对话记录。" /> : <List dataSource={messages} renderItem={(message) => <MessageItem message={message} />} />}
        </Spin>
      </Card>
    </section>
  );
}
