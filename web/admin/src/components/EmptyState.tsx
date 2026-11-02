import { Empty, Spin } from "antd";

interface EmptyStateProps {
  title: string;
  description: string;
}

export function EmptyState({ title, description }: EmptyStateProps) {
  return (
    <div className="empty-state">
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={(
          <div>
            <h3>{title}</h3>
            <p>{description}</p>
          </div>
        )}
      />
    </div>
  );
}

interface LoadingRowsProps {
  label?: string;
}

export function LoadingRows({ label = "正在读取数据" }: LoadingRowsProps) {
  return (
    <div className="loading-state" role="status">
      <Spin size="small" />
      <span>{label}</span>
    </div>
  );
}
