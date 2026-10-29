import type { Conversation, MemoryStatus, Sticker, UpdateResponse } from "./types";

const API_ROOT = "/admin/api";

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

function errorMessage(payload: unknown, status: number): string {
  if (payload && typeof payload === "object" && "error" in payload) {
    const value = (payload as { error?: unknown }).error;
    if (typeof value === "string" && value.trim()) {
      return value;
    }
  }
  return `请求失败（HTTP ${status}）`;
}

export class AdminApi {
  constructor(private readonly token: string) {}

  private async response(path: string, init: RequestInit = {}): Promise<Response> {
    const headers = new Headers(init.headers);
    if (this.token) {
      headers.set("Authorization", `Bearer ${this.token}`);
    }
    if (init.body && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json");
    }

    const response = await fetch(`${API_ROOT}${path}`, {
      ...init,
      headers,
      credentials: "same-origin",
    });
    if (response.ok) {
      return response;
    }

    const payload = await response.json().catch(() => null);
    throw new ApiError(response.status, errorMessage(payload, response.status));
  }

  private async json<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await this.response(path, {
      ...init,
      headers: {
        Accept: "application/json",
        ...init.headers,
      },
    });
    return (await response.json()) as T;
  }

  getGroups(signal?: AbortSignal): Promise<number[]> {
    return this.json<number[]>("/groups", { signal });
  }

  getConversation(group: string, signal?: AbortSignal): Promise<Conversation> {
    return this.json<Conversation>(`/conversation?group=${encodeURIComponent(group)}`, { signal });
  }

  saveNote(
    noteId: string,
    body: { group: number; version: number; text: string; forget: boolean },
  ): Promise<UpdateResponse> {
    return this.json<UpdateResponse>(`/notes/${encodeURIComponent(noteId)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    });
  }

  getStickers(signal?: AbortSignal): Promise<Sticker[]> {
    return this.json<Sticker[]>("/stickers", { signal });
  }

  getMemory(signal?: AbortSignal): Promise<MemoryStatus> {
    return this.json<MemoryStatus>("/memory", { signal });
  }

  saveSticker(
    stickerId: string,
    body: { version: number; description: string; blocked: boolean },
  ): Promise<UpdateResponse> {
    return this.json<UpdateResponse>(`/stickers/${encodeURIComponent(stickerId)}`, {
      method: "PATCH",
      body: JSON.stringify(body),
    });
  }

  async getStickerImage(stickerId: string, signal?: AbortSignal): Promise<Blob> {
    const response = await this.response(`/stickers/${encodeURIComponent(stickerId)}/image`, {
      headers: { Accept: "image/*" },
      signal,
    });
    return response.blob();
  }
}
