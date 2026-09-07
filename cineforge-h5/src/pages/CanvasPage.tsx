import React from "react";
import { getPlatformToken, getSessionExpiresAt, getTapToken } from "../api";
import { createCineForgeSsoSessionMessage } from "../ssoProtocol";

/** 画布项目嵌入地址（由 vite.config.ts 中 define.__CANVAS_URL__ 注入） */
declare const __CANVAS_URL__: string;
const DEFAULT_REACT_FLOW_CANVAS_URL: string = __CANVAS_URL__;

/** 外部链接图标（内联 SVG，替代 @tabler/icons-react） */
const ExternalLinkIcon = ({ size = 14 }: { size?: number }) => (
  <svg
    width={size}
    height={size}
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2"
    strokeLinecap="round"
    strokeLinejoin="round"
    aria-hidden="true"
  >
    <path d="M15 3h6v6" />
    <path d="M10 14 21 3" />
    <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
  </svg>
);

/** 按钮基础样式 */
const buttonBaseStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 6,
  padding: "6px 14px",
  fontSize: 13,
  fontWeight: 500,
  borderRadius: 6,
  cursor: "pointer",
  border: "1px solid rgba(15, 23, 42, 0.12)",
  background: "#ffffff",
  color: "#0f172a",
  transition: "background 0.15s ease",
};

export function CanvasPage({ src }: { src?: string }) {
  const REACT_FLOW_CANVAS_URL: string = src || DEFAULT_REACT_FLOW_CANVAS_URL;
  const iframeRef = React.useRef<HTMLIFrameElement>(null);
  const [iframeLoaded, setIframeLoaded] = React.useState(false);
  const [loadError, setLoadError] = React.useState(false);

  /** 在新标签页中打开 React-Flow 画布 */
  const handleOpenExternal = React.useCallback(() => {
    window.open(REACT_FLOW_CANVAS_URL, "_blank", "noopener,noreferrer");
  }, []);

  /** 刷新 iframe */
  const handleRefresh = React.useCallback(() => {
    setIframeLoaded(false);
    setLoadError(false);
    if (iframeRef.current) {
      const src = iframeRef.current.src;
      iframeRef.current.src = "about:blank";
      setTimeout(() => {
        if (iframeRef.current) {
          iframeRef.current.src = src;
        }
      }, 50);
    }
  }, []);

  /** iframe 加载完成 */
  const handleIframeLoad = React.useCallback(() => {
    setIframeLoaded(true);
    setLoadError(false);

    const tapcanvasToken = getTapToken();
    const platformToken = getPlatformToken();
    const sessionExpiresAt = getSessionExpiresAt();
    if (tapcanvasToken && platformToken && sessionExpiresAt && iframeRef.current?.contentWindow) {
      const targetOrigin = new URL(REACT_FLOW_CANVAS_URL, window.location.origin).origin;
      const targetPath = REACT_FLOW_CANVAS_URL.includes("/workbench")
        ? `${new URL(REACT_FLOW_CANVAS_URL, window.location.origin).pathname}${new URL(REACT_FLOW_CANVAS_URL, window.location.origin).search}`
        : undefined;
      iframeRef.current.contentWindow.postMessage(
        createCineForgeSsoSessionMessage(tapcanvasToken, platformToken, sessionExpiresAt, targetPath),
        targetOrigin,
      );
    }
  }, []);

  /** iframe 加载失败兜底提示 */
  React.useEffect(() => {
    if (!iframeLoaded) {
      const timer = window.setTimeout(() => {
        setLoadError(true);
      }, 15000);
      return () => window.clearTimeout(timer);
    }
    return;
  }, [iframeLoaded]);

  return (
    <div
      style={{
        width: "100%",
        height: "100%",
        overflow: "hidden",
        position: "relative",
      }}
    >
      {/* 加载中提示 */}
      {!iframeLoaded && !loadError && (
        <div
          style={{
            position: "absolute",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 10,
            background: "rgba(248, 250, 252, 0.95)",
          }}
        >
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              alignItems: "center",
              gap: 12,
            }}
          >
            <style>{`
              @keyframes canvas-spin {
                to { transform: rotate(360deg); }
              }
            `}</style>
            <span
              style={{
                width: 24,
                height: 24,
                border: "3px solid rgba(15, 23, 42, 0.1)",
                borderTopColor: "#3b82f6",
                borderRadius: "50%",
                animation: "canvas-spin 0.8s linear infinite",
              }}
            />
            <span style={{ fontSize: 13, color: "#64748b" }}>
              画布加载中...
            </span>
          </div>
        </div>
      )}

      {/* 错误兜底提示 */}
      {loadError && !iframeLoaded && (
        <div
          style={{
            position: "absolute",
            inset: 0,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 10,
            background: "rgba(248, 250, 252, 0.95)",
          }}
        >
          <div
            style={{
              background: "#ffffff",
              border: "1px solid rgba(15, 23, 42, 0.08)",
              borderRadius: 8,
              padding: "24px 32px",
              maxWidth: 480,
              textAlign: "center",
              boxShadow: "0 4px 12px rgba(15, 23, 42, 0.06)",
            }}
          >
            <div
              style={{
                fontSize: 16,
                fontWeight: 600,
                color: "#dc2626",
                marginBottom: 8,
              }}
            >
              画布加载失败
            </div>
            <div
              style={{
                fontSize: 13,
                color: "#64748b",
                lineHeight: 1.6,
                marginBottom: 16,
              }}
            >
              无法连接到 React-Flow 画布项目：
              <br />
              <a
                href={REACT_FLOW_CANVAS_URL}
                target="_blank"
                rel="noopener noreferrer"
                style={{ color: "#2563eb", textDecoration: "underline" }}
              >
                {REACT_FLOW_CANVAS_URL}
              </a>
              <br />
              <br />
              请确认 React-Flow 项目已启动且运行在上述地址。
              若端口不一致，可修改 vite.config.ts 中的
              define.__CANVAS_URL__ 配置。
            </div>
            <div
              style={{
                display: "flex",
                gap: 8,
                justifyContent: "center",
              }}
            >
              <button
                type="button"
                onClick={handleRefresh}
                style={buttonBaseStyle}
              >
                重试
              </button>
              <button
                type="button"
                onClick={handleOpenExternal}
                style={buttonBaseStyle}
              >
                <ExternalLinkIcon size={14} />
                新窗口打开
              </button>
            </div>
          </div>
        </div>
      )}

      <iframe
        ref={iframeRef}
        src={REACT_FLOW_CANVAS_URL}
        title="React-Flow Canvas Workbench"
        onLoad={handleIframeLoad}
        style={{
          width: "100%",
          height: "100%",
          border: "none",
          display: "block",
          background: "#ffffff",
        }}
        allow="clipboard-read; clipboard-write; fullscreen"
      />
    </div>
  );
}

export default CanvasPage;
