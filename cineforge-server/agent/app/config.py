"""CineForge agent 子模块配置。

字段名与 legacy `backend/app/config.py` 保持一致（整层搬用无需改名），
但环境注入通道切到 CineForge 部署的 `CINEFORGE_*`（与 Go 单体同源）：

  - CINEFORGE_DB_DSN          → database_url（postgres:// → postgresql+asyncpg:// 自动换算）
  - CINEFORGE_REDIS_URL       → redis_url（默认 redis://127.0.0.1:6379/0）
  - CINEFORGE_MINIO_*         → minio_*
  - CINEFORGE_HERMES_*        → hermes_*（ADDR 即 base_url）
  - CINEFORGE_AUTH_SECRET     → auth_secret（security.password_hash 加盐）
  - CINEFORGE_SERVER_DOMAIN   → Hermes dashboard/webui URL 域名
"""

from __future__ import annotations

import os

from pydantic import model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

# CINEFORGE_*（部署注入通道）→ 本模块字段对应的 legacy env 名。
# 仅当字段环境变量未显式设置时映射，显式设置优先。
_CINEFORGE_ENV_MAP: dict[str, str] = {
    "CINEFORGE_DB_DSN": "DATABASE_URL",
    "CINEFORGE_REDIS_URL": "REDIS_URL",
    "CINEFORGE_MINIO_ENDPOINT": "MINIO_ENDPOINT",
    "CINEFORGE_MINIO_BUCKET": "MINIO_BUCKET",
    "CINEFORGE_MINIO_ACCESS_KEY": "MINIO_ACCESS_KEY",
    "CINEFORGE_MINIO_SECRET_KEY": "MINIO_SECRET_KEY",
    "CINEFORGE_MINIO_USE_SSL": "MINIO_SECURE",
    "CINEFORGE_HERMES_ADDR": "HERMES_BASE_URL",
    "CINEFORGE_HERMES_HEALTH_URL": "HERMES_HEALTH_URL",
    "CINEFORGE_HERMES_MODEL": "HERMES_MODEL",
    "CINEFORGE_HERMES_API_KEY": "HERMES_API_KEY",
    "CINEFORGE_HERMES_ENABLED": "HERMES_ENABLED",
    "CINEFORGE_HERMES_DASHBOARD_URL": "HERMES_DASHBOARD_URL",
    "CINEFORGE_HERMES_WEBUI_URL": "HERMES_WEBUI_URL",
    "CINEFORGE_AUTH_SECRET": "AUTH_SECRET",
    "CINEFORGE_SERVER_DOMAIN": "RAN_SERVER_HOST",
}
for _src, _dst in _CINEFORGE_ENV_MAP.items():
    if _src in os.environ and _dst not in os.environ:
        os.environ[_dst] = os.environ[_src]


class Settings(BaseSettings):
    database_url: str = "postgresql+asyncpg://cineforge:CHANGE_ME@127.0.0.1:5432/cineforge"
    redis_url: str = "redis://127.0.0.1:6379/0"
    minio_endpoint: str = "127.0.0.1:9000"
    minio_access_key: str = "cineforge"
    minio_secret_key: str = "dev_local_minio_pw"
    minio_bucket: str = "cineforge-assets"
    minio_secure: bool = False
    agent_payload_externalize_threshold_bytes: int = 256 * 1024
    # Hermes（模型网关）：base_url 含 /v1 段
    hermes_base_url: str = "http://127.0.0.1:8642/v1"
    hermes_health_url: str = "http://127.0.0.1:8642/health"
    hermes_api_key: str = "change-me-hermes-local"
    hermes_model: str = "hermes-agent"
    hermes_dashboard_url: str = ""
    hermes_webui_url: str = ""
    hermes_enabled: bool = True
    hermes_timeout_seconds: float = 400.0
    hermes_artifact_dir: str = "/hermes-data"
    hermes_artifact_max_bytes: int = 2 * 1024 * 1024
    hermes_io_dump_enabled: bool = False
    hermes_io_dump_dir: str = "logs/hermes_io"
    script_reading_timeout_seconds: float = 780.0
    runtime_policy_cache_enabled: bool = True
    runtime_policy_cache_ttl_seconds: int = 7 * 24 * 60 * 60
    runtime_policy_cache_timeout_seconds: float = 0.25
    runtime_policy_cache_circuit_seconds: float = 30.0
    runtime_policy_cache_max_payload_bytes: int = 512 * 1024
    redis_max_connections: int = 20
    redis_socket_timeout_seconds: float = 0.25
    project_status_cache_enabled: bool = True
    project_status_cache_ttl_seconds: int = 5
    workflow_lock_enabled: bool = True
    workflow_lock_ttl_seconds: int = 35 * 60
    runtime_policy_retry_base_seconds: float = 0.5
    runtime_policy_max_elapsed_seconds: float = 420.0
    script_reading_max_elapsed_seconds: float = 850.0
    agent_prompt_concurrency: int = 3
    asset_prompt_concurrency: int = 5
    storyboard_batch_size: int = 1
    storyboard_batch_concurrency: int = 3
    langgraph_checkpoint_enabled: bool = False
    auth_secret: str = "change-me-cineforge-auth-secret"
    # 生产服务器 IP（Hermes dashboard/webui 默认域名）
    ran_server_host: str = "cine-forge-server.cnjm.top"

    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    @model_validator(mode="after")
    def _normalize_and_fill(self) -> "Settings":
        """数据库 DSN 统一 asyncpg 方言；未配置 dashboard/webui 时按域名拼接。"""
        if self.database_url.startswith("postgres://") or self.database_url.startswith("postgresql://"):
            self.database_url = self.database_url.replace(
                "postgresql://", "postgresql+asyncpg://", 1
            ).replace("postgres://", "postgresql+asyncpg://", 1)
        if not self.hermes_dashboard_url and self.ran_server_host:
            self.hermes_dashboard_url = f"http://{self.ran_server_host}:9119"
        if not self.hermes_webui_url and self.ran_server_host:
            self.hermes_webui_url = f"http://{self.ran_server_host}:3000"
        return self


settings = Settings()