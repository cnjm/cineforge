/**
 * TapCanvas 微服务 API 客户端。
 * 所有请求走 /tapapi/* 前缀，由 Nginx 反代到 tap-api:8788。
 * 认证使用 tapcanvas_token（由 cine-forge 后端登录时签发）。
 */
import { getTapToken, setTapToken } from "./api";

/**
 * TapCanvas 基址：由 .env 的 VITE_TAPAPI_BASE 注入。
 * 留空 = 同源 /tapapi（本地 vite proxy / 同域部署场景）。
 */
const TAPAPI_BASE = String(import.meta.env.VITE_TAPAPI_BASE || "").trim().replace(/\/+$/, "");

/** 从 tap-api 响应中提取可读错误消息（优先 JSON 的 message/detail/error 字段）。 */
function tapErrorMessage(response: Response, body: string): string {
  try {
    const data: unknown = JSON.parse(body);
    if (data && typeof data === "object") {
      const record = data as Record<string, unknown>;
      const candidate = record.message ?? record.detail ?? record.error;
      if (typeof candidate === "string" && candidate) return candidate;
    }
  } catch {
    // 非 JSON 响应，回退到原始 body。
  }
  return body || `${response.status} ${response.statusText}`;
}

async function tapRequest<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getTapToken();
  const headers: HeadersInit = {
    ...(init?.headers ?? {}),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
  let response: Response;
  try {
    response = await fetch(`${TAPAPI_BASE}/tapapi${path}`, { ...init, headers });
  } catch (error) {
    throw new Error(error instanceof TypeError ? "无法连接 TapCanvas 服务" : "请求失败");
  }
  if (!response.ok) {
    const body = await response.text();
    if (response.status === 401) {
      // 仅清除 tapcanvas token；不销毁仍有效的 cineforge 会话（避免误伤整站登录态）。
      setTapToken(null);
      throw new Error("TapCanvas 会话已失效，请重新登录");
    }
    throw new Error(tapErrorMessage(response, body));
  }
  if (response.status === 204) return undefined as T;
  return response.json();
}

export const tapApi = {
  get<T>(path: string): Promise<T> {
    return tapRequest<T>(path);
  },
  post<T>(path: string, body?: unknown): Promise<T> {
    return tapRequest<T>(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    });
  },
  patch<T>(path: string, body?: unknown): Promise<T> {
    return tapRequest<T>(path, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
    });
  },
  delete<T>(path: string): Promise<T> {
    return tapRequest<T>(path, { method: "DELETE" });
  },
};
