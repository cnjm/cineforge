import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import { resolve } from "path";

export default defineConfig(({ mode }) => {
  // 从 .env* 读取配置（前端无 process.env，统一走 Vite env 通道）
  const env = loadEnv(mode, process.cwd(), "");
  return {
    plugins: [react()],
    define: {
      // 画布项目嵌入地址（React-Flow 画布项目运行地址），.env 的 VITE_CANVAS_URL 可覆盖
      __CANVAS_URL__: JSON.stringify(env.VITE_CANVAS_URL || "http://localhost:5174"),
    },
    server: {
      host: "0.0.0.0",
      port: 5173,
      proxy: {
        // 本地开发反代目标：默认本地后端；可用环境变量覆盖：
        // VITE_CINEFORGE_API_PROXY（CineForge 后端）/ VITE_TAPAPI_PROXY（TapCanvas 后端）
        "/api": env.VITE_CINEFORGE_API_PROXY?.trim() || "http://localhost:8000",
        // TapCanvas 后端（开发时通过本地 hono-api 或 Docker 映射端口）
        "/tapapi": {
          target: env.VITE_TAPAPI_PROXY?.trim() || "http://localhost:8788",
          rewrite: (path) => path.replace(/^\/tapapi/, ""),
        },
      },
    },
    build: {
      rollupOptions: {
        input: {
          main: resolve(__dirname, "index.html"),
        },
      },
    },
  };
});