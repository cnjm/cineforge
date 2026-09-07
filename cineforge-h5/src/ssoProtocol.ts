export const CINEFORGE_SSO_SESSION_MESSAGE = "CINEFORGE_SSO_SESSION_V1" as const;

export type CineForgeSsoSessionMessage = {
  type: typeof CINEFORGE_SSO_SESSION_MESSAGE;
  tapcanvas_token: string;
  platform_token: string;
  session_expires_at: number;
  source: "cine-forge";
  target_path?: string;
};

export function createCineForgeSsoSessionMessage(
  tapcanvasToken: string,
  platformToken: string,
  sessionExpiresAt: number,
  targetPath?: string,
): CineForgeSsoSessionMessage {
  if (!tapcanvasToken || !platformToken || !Number.isFinite(sessionExpiresAt)) {
    throw new Error("complete SSO session is required");
  }
  return {
    type: CINEFORGE_SSO_SESSION_MESSAGE,
    tapcanvas_token: tapcanvasToken,
    platform_token: platformToken,
    session_expires_at: sessionExpiresAt,
    source: "cine-forge",
    ...(targetPath ? { target_path: targetPath } : {}),
  };
}
