import type { AuthSession, FileDownloadResponse, FilePlaybackResponse, FileUploadResponse, Project, ProjectImportPayload, User, UserRole } from "./types";

const TOKEN_KEY = "cineforge_token";
const CURRENT_USER_KEY = "cineforge_current_user";
const TAP_TOKEN_KEY = "tapcanvas_token";
const PLATFORM_TOKEN_KEY = "platform_token";
const SESSION_EXPIRES_AT_KEY = "session_expires_at";

/**
 * 后端 API 基址：由 .env 的 VITE_API_BASE 注入（OSS 静态托管 + 独立 API 域名场景）。
 * 留空 = 同源 /api（本地 vite proxy / 同域部署场景）。
 */
export const API_BASE = String(import.meta.env.VITE_API_BASE || "").trim().replace(/\/+$/, "");

export function safeStorageGet(key: string) {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function safeStorageSet(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable in private or restricted browser modes.
  }
}

export function safeStorageRemove(key: string) {
  try {
    window.localStorage.removeItem(key);
  } catch {
    // Ignore storage cleanup failures.
  }
}

let authToken = safeStorageGet(TOKEN_KEY) || "";

export class AuthExpiredError extends Error {
  constructor(message = "登录已过期，请重新登录") {
    super(message);
    this.name = "AuthExpiredError";
  }
}

export function getAuthToken() {
  return authToken;
}

export function readStoredUser(): User | null {
  const raw = safeStorageGet(CURRENT_USER_KEY);
  if (!raw) return null;
  try {
    return normalizeUser(JSON.parse(raw));
  } catch {
    safeStorageRemove(CURRENT_USER_KEY);
    return null;
  }
}

export function normalizeUser(raw: unknown): User {
  if (!raw || typeof raw !== "object") throw new Error("Invalid stored user");
  const data = raw as Partial<User> & { name?: string };
  const id = String(data.id || data.phone || data.username || "");
  const displayName = String(data.display_name || data.name || data.username || data.phone || "用户");
  if (!id) throw new Error("Invalid stored user");
  return {
    id,
    username: data.username ? String(data.username) : undefined,
    phone: data.phone ? String(data.phone) : undefined,
    display_name: displayName,
    role: (["director", "script_editor", "artist", "editor", "admin"].includes(String(data.role)) ? String(data.role) : "artist") as UserRole,
    is_active: data.is_active,
  };
}

export function setAuthSession(session: AuthSession | null) {
  authToken = session?.token || "";
  if (session) {
    safeStorageSet(TOKEN_KEY, session.token);
    safeStorageSet(CURRENT_USER_KEY, JSON.stringify(session.user));
    setTapToken(session.tapcanvas_token);
    setPlatformToken(session.platform_token);
    safeStorageSet(SESSION_EXPIRES_AT_KEY, String(session.session_expires_at));
  } else {
    safeStorageRemove(TOKEN_KEY);
    safeStorageRemove(CURRENT_USER_KEY);
    setTapToken(null);
    setPlatformToken(null);
    safeStorageRemove(SESSION_EXPIRES_AT_KEY);
  }
}

/** 读取 TapCanvas token（由 cine-forge 登录时签发） */
export function getTapToken(): string {
  return safeStorageGet(TAP_TOKEN_KEY) || "";
}

/** 保存/清除 TapCanvas token */
export function setTapToken(token: string | null) {
  if (token) {
    safeStorageSet(TAP_TOKEN_KEY, token);
  } else {
    safeStorageRemove(TAP_TOKEN_KEY);
  }
}

export function getPlatformToken(): string {
  return safeStorageGet(PLATFORM_TOKEN_KEY) || "";
}

export function setPlatformToken(token: string | null) {
  if (token) {
    safeStorageSet(PLATFORM_TOKEN_KEY, token);
  } else {
    safeStorageRemove(PLATFORM_TOKEN_KEY);
  }
}

export function getSessionExpiresAt(): number | null {
  const raw = safeStorageGet(SESSION_EXPIRES_AT_KEY);
  if (!raw) return null;
  const value = Number(raw);
  return Number.isFinite(value) && value > 0 ? value : null;
}

function authHeaders(json = false): HeadersInit {
  return {
    ...(json ? { "Content-Type": "application/json" } : {}),
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
  };
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  // 请求前预检查：如果本地会话已过期，直接抛出 AuthExpiredError 避免无效请求
  if (isSessionExpired()) {
    setAuthSession(null);
    throw new AuthExpiredError();
  }
  let response: Response;
  try {
    response = await fetch(`${API_BASE}/api${path}`, init);
  } catch (error) {
    throw new Error(error instanceof TypeError ? "无法连接后端服务，请确认 API 服务已启动" : "请求失败");
  }
  if (!response.ok) {
    const message = await responseMessage(response);
    if (response.status === 401) {
      setAuthSession(null);
      throw new AuthExpiredError();
    }
    // 对 403、422、500 等状态，若消息中包含过期/认证关键字，也判定为登录失效
    if (isExpiredAuthMessage(message) || isExpiredAuthMessage(response.statusText)) {
      setAuthSession(null);
      throw new AuthExpiredError(message);
    }
    throw new Error(message);
  }
  if (response.status === 204) return undefined as T;
  return response.json();
}

function isExpiredAuthMessage(message: string) {
  return /token expired|登录已过期|expired|invalid token|not authenticated|未登录|会话已过期|session expired|凭证已过期|认证失败|请重新登录/i.test(message);
}

async function responseMessage(response: Response) {
  const fallback = `${response.status} ${response.statusText}`;
  try {
    const data = await response.json();
    if (data && typeof data === "object" && "detail" in data) {
      const detail = (data as { detail?: unknown }).detail;
      return typeof detail === "string" ? detail : JSON.stringify(detail);
    }
  } catch {
    // Non-JSON errors fall back to the HTTP status.
  }
  return fallback;
}

/** 判断当前本地会话是否已过期（基于 session_expires_at） */
export function isSessionExpired(): boolean {
  const expiresAt = getSessionExpiresAt();
  if (!expiresAt) return false;
  return Date.now() >= expiresAt * 1000;
}

export const api = {
  async get<T>(path: string): Promise<T> {
    return request<T>(path, { headers: authHeaders() });
  },
  async post<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, {
      method: "POST",
      headers: authHeaders(true),
      body: body ? JSON.stringify(body) : undefined,
    });
  },
  async patch<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, {
      method: "PATCH",
      headers: authHeaders(true),
      body: body ? JSON.stringify(body) : undefined,
    });
  },
  async delete<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, {
      method: "DELETE",
      headers: authHeaders(true),
      body: body ? JSON.stringify(body) : undefined,
    });
  },
  async uploadTaskFile(taskId: string, file: File, onProgress?: (progress: number) => void): Promise<FileUploadResponse> {
    if (isSessionExpired()) {
      setAuthSession(null);
      return Promise.reject(new AuthExpiredError());
    }
    const form = new FormData();
    form.append("task_id", taskId);
    form.append("file_size", String(file.size));
    form.append("file", file);
    return new Promise((resolve, reject) => {
      const request = new XMLHttpRequest();
      request.open("POST", `${API_BASE}/api/files/task-upload`);
      if (authToken) request.setRequestHeader("Authorization", `Bearer ${authToken}`);
      request.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable) onProgress?.(Math.min(100, Math.round((event.loaded / event.total) * 100)));
      });
      request.addEventListener("error", () => reject(new Error("无法连接后端服务，请确认 API 服务已启动")));
      request.addEventListener("abort", () => reject(new Error("上传已取消")));
      request.addEventListener("load", () => {
        if (request.status >= 200 && request.status < 300) {
          try {
            onProgress?.(100);
            resolve(JSON.parse(request.responseText) as FileUploadResponse);
          } catch {
            reject(new Error("上传响应格式无效"));
          }
          return;
        }
        const message = xhrResponseMessage(request);
        if (request.status === 401) {
          setAuthSession(null);
          reject(new AuthExpiredError());
          return;
        }
        if (isExpiredAuthMessage(message) || isExpiredAuthMessage(request.statusText)) {
          setAuthSession(null);
          reject(new AuthExpiredError(message));
          return;
        }
        reject(new Error(message));
      });
      request.send(form);
    });
  },
  async getFilePlaybackUrl(fileId: string, signal?: AbortSignal): Promise<FilePlaybackResponse> {
    return request<FilePlaybackResponse>(`/files/${fileId}/playback-url`, {
      headers: authHeaders(),
      signal,
    });
  },
  async getFileDownloadUrl(fileId: string, fileName?: string): Promise<FileDownloadResponse> {
    const params = new URLSearchParams();
    if (fileName) params.set("file_name", fileName);
    const query = params.toString();
    return request<FileDownloadResponse>(`/files/${fileId}/download-url${query ? `?${query}` : ""}`, {
      headers: authHeaders(),
    });
  },
  async importProject(payload: ProjectImportPayload, file: File): Promise<Project> {
    if (isSessionExpired()) {
      setAuthSession(null);
      throw new AuthExpiredError();
    }
    const params = new URLSearchParams();
    params.set("title", payload.title);
    params.set("project_prefix", payload.project_prefix);
    if (payload.genre) params.set("genre", payload.genre);
    if (payload.project_id) params.set("project_id", payload.project_id);
    if (payload.production_brief) params.set("production_brief", JSON.stringify(payload.production_brief));
    if (payload.episode_brief) params.set("episode_brief", JSON.stringify(payload.episode_brief));
    if (payload.episode_no) params.set("episode_no", String(payload.episode_no));
    if (payload.confirm_existing_episode_version) params.set("confirm_existing_episode_version", "true");
    params.set("filename", file.name || "script.txt");
    const response = await fetch(`${API_BASE}/api/projects/import?${params.toString()}`, {
      method: "POST",
      headers: {
        ...authHeaders(),
        "Content-Type": "application/octet-stream",
      },
      body: file,
    });
    if (!response.ok) {
      const message = await responseMessage(response);
      if (response.status === 401) {
        setAuthSession(null);
        throw new AuthExpiredError();
      }
      if (isExpiredAuthMessage(message) || isExpiredAuthMessage(response.statusText)) {
        setAuthSession(null);
        throw new AuthExpiredError(message);
      }
      throw new Error(message);
    }
    return response.json();
  },
};

function xhrResponseMessage(request: XMLHttpRequest) {
  const fallback = `${request.status} ${request.statusText}`.trim();
  try {
    const data = JSON.parse(request.responseText) as { detail?: unknown };
    if (typeof data.detail === "string") return data.detail;
    if (data.detail !== undefined) return JSON.stringify(data.detail);
  } catch {
    // Non-JSON errors fall back to the HTTP status.
  }
  return fallback || "上传失败";
}
