import { getPlatformToken } from "./api";

const configuredBase = String(import.meta.env.VITE_GO_API_BASE || "").trim();
export const GO_API_BASE = configuredBase.replace(/\/+$/, "");

export class GoApiConfigurationError extends Error {
  constructor() {
    super("VITE_GO_API_BASE is required for platform API requests");
    this.name = "GoApiConfigurationError";
  }
}

export async function platformFetch(path: string, init: RequestInit = {}): Promise<Response> {
  if (!GO_API_BASE) throw new GoApiConfigurationError();
  const token = getPlatformToken();
  if (!token) throw new Error("platform session is missing");
  return fetch(`${GO_API_BASE}/api/v1${path}`, {
    ...init,
    headers: {
      ...(init.headers || {}),
      Authorization: `Bearer ${token}`,
    },
  });
}
