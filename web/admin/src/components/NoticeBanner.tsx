import type { AdminNotice } from "../types";

interface NoticeBannerProps {
  notice: AdminNotice;
  onDismiss: () => void;
}

export function NoticeBanner({ notice, onDismiss }: NoticeBannerProps) {
  return (
    <div
      className={`notice-banner notice-${notice.tone}`}
      role={notice.tone === "error" ? "alert" : "status"}
      aria-live="polite"
    >
      <span className="notice-dot" aria-hidden="true" />
      <p>{notice.message}</p>
      <button className="notice-close" type="button" onClick={onDismiss} aria-label="关闭提示">
        ×
      </button>
    </div>
  );
}
