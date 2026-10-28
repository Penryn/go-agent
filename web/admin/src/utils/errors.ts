import { ApiError } from "../api";
import type { AdminNotice } from "../types";

function rawMessage(error: unknown): string {
  if (error instanceof Error && error.message.trim()) {
    return error.message;
  }
  return "未知错误";
}

export function noticeForError(error: unknown, fallback: string): AdminNotice {
  if (error instanceof ApiError) {
    if (error.status === 401) {
      return {
        tone: "error",
        message: "鉴权失败：访问令牌无效或已变更。请重新输入令牌并连接。",
      };
    }
    if (error.status === 403) {
      return {
        tone: "error",
        message: `当前访问被拒绝：${error.message}。请检查访问令牌和本机访问限制。`,
      };
    }
    if (error.status === 409) {
      return { tone: "error", message: error.message };
    }
    return { tone: "error", message: error.message || fallback };
  }
  if (error instanceof DOMException && error.name === "AbortError") {
    return { tone: "info", message: "请求已取消。" };
  }
  return {
    tone: "error",
    message: error instanceof Error ? `请求失败：${rawMessage(error)}` : fallback,
  };
}
