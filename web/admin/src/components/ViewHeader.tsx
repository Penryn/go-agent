import type { ReactNode } from "react";
import { Badge, Typography } from "antd";

interface ViewHeaderProps {
  eyebrow: string;
  title: string;
  description: string;
  count?: number;
  action?: ReactNode;
}

export function ViewHeader({ eyebrow, title, description, count, action }: ViewHeaderProps) {
  return (
    <header className="view-header">
      <div>
        <Typography.Text className="eyebrow">{eyebrow}</Typography.Text>
        <div className="view-title-row">
          <Typography.Title level={2}>{title}</Typography.Title>
          {count !== undefined ? <Badge count={count} showZero color="#1677ff" /> : null}
        </div>
        <Typography.Paragraph className="view-description">{description}</Typography.Paragraph>
      </div>
      {action ? <div className="view-action">{action}</div> : null}
    </header>
  );
}
