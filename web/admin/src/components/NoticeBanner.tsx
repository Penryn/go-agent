import type { AdminNotice } from "../types";
import { Alert } from "antd";

interface NoticeBannerProps {
  notice: AdminNotice;
  onDismiss: () => void;
}

export function NoticeBanner({ notice, onDismiss }: NoticeBannerProps) {
  return (
    <Alert
      className={`notice-banner notice-${notice.tone}`}
      type={notice.tone}
      message={notice.message}
      showIcon
      closable
      onClose={onDismiss}
    />
  );
}
