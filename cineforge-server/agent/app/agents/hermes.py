from __future__ import annotations

import json
import logging
import re
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import quote

import httpx
from jsonschema import Draft202012Validator

from app.config import settings


logger = logging.getLogger(__name__)


_JSON_DELIVERY_POLICY = (
    "当前调用是无工具的 JSON API 调用。禁止调用任何工具，包括 skill_view、todo、terminal、"
    "write_file、read_file 或其他函数；也禁止先在中间 assistant 消息生成 JSON 后再调用工具。"
    "不得写入文件或只返回文件路径、交付说明、摘要、统计。"
    "完整 JSON 必须直接放在最终 assistant message.content 中；响应第一个字符必须是 {，最后一个字符必须是 }。"
)


class HermesClient:
    def __init__(self) -> None:
        self.base_url = settings.hermes_base_url.rstrip("/")
        self.health_url = settings.hermes_health_url
        self.api_key = settings.hermes_api_key
        self.model = settings.hermes_model
        self.timeout = settings.hermes_timeout_seconds
        self._json_schema_supported: bool | None = None

    async def health(self) -> dict[str, Any]:
        result: dict[str, Any] = {
            "enabled": settings.hermes_enabled,
            "base_url": self.base_url,
            "health_url": self.health_url,
            "model": self.model,
            "dashboard_url": settings.hermes_dashboard_url,
            "webui_url": settings.hermes_webui_url,
            "status": "disabled" if not settings.hermes_enabled else "unknown",
        }
        if not settings.hermes_enabled:
            return result

        async with httpx.AsyncClient(timeout=min(self.timeout, 8.0)) as client:
            try:
                health_response = await client.get(self.health_url)
                result["health_status_code"] = health_response.status_code
                result["health"] = _safe_json(health_response)
                result["status"] = "ok" if health_response.is_success else "unhealthy"
            except httpx.HTTPError as exc:
                result["status"] = "unreachable"
                result["error"] = str(exc)
                return result

            try:
                models_response = await client.get(
                    f"{self.base_url}/models",
                    headers=self._headers(),
                )
                result["models_status_code"] = models_response.status_code
                result["models"] = _safe_json(models_response)
                if not models_response.is_success:
                    result["status"] = "auth_failed" if models_response.status_code == 401 else "unhealthy"
            except httpx.HTTPError as exc:
                result["models_error"] = str(exc)
        return result

    async def chat_json(
        self,
        *,
        system: str,
        user: str,
        timeout_seconds: float | None = None,
        json_schema: dict[str, Any] | None = None,
        schema_name: str | None = None,
    ) -> dict[str, Any]:
        if not settings.hermes_enabled:
            raise RuntimeError("Hermes is disabled")

        use_json_schema = bool(json_schema) and self._json_schema_supported is not False
        requested_session_id = f"cineforge-{uuid.uuid4().hex}"
        request_headers = {
            **self._headers(),
            "X-Hermes-Session-Id": requested_session_id,
        }
        body: dict[str, Any] = {
            "model": self.model,
            "messages": [
                {"role": "system", "content": _json_delivery_system_prompt(system)},
                {"role": "user", "content": user},
            ],
            "tools": [],
            "tool_choice": "none",
            "temperature": 0.2,
            "max_tokens": 8192,  # 提升到 8192，避免长剧本输出被截断
            "stream": False,
            "response_format": self._response_format(json_schema, schema_name) if use_json_schema else {"type": "json_object"},
        }
        request_started_at = time.time()
        request_timeout = timeout_seconds or self.timeout
        logger.info(
            "hermes.chat_json 请求 model=%s schema_name=%s use_json_schema=%s "
            "session_id=%s timeout=%s system_len=%d user_len=%d",
            self.model,
            schema_name,
            use_json_schema,
            requested_session_id,
            request_timeout,
            len(system or ""),
            len(user or ""),
        )
        _dump_hermes_io("request", requested_session_id, body)
        async with httpx.AsyncClient(timeout=request_timeout) as client:
            try:
                response = await client.post(
                    f"{self.base_url}/chat/completions",
                    headers=request_headers,
                    json=body,
                )
                if use_json_schema and response.status_code in {400, 422} and _schema_format_rejected(response.text):
                    logger.warning(
                        "hermes.chat_json 服务端拒绝 json_schema 响应格式，降级为 json_object 重试 "
                        "schema_name=%s status=%s detail=%s",
                        schema_name,
                        response.status_code,
                        response.text,
                    )
                    self._json_schema_supported = False
                    body["response_format"] = {"type": "json_object"}
                    response = await client.post(
                        f"{self.base_url}/chat/completions",
                        headers=request_headers,
                        json=body,
                    )
                logger.info(
                    "hermes.chat_json 响应 url=%s status=%s elapsed_ms=%s "
                    "encoding=%s session_id=%s",
                    str(response.url),
                    response.status_code,
                    response.elapsed.total_seconds() * 1000,
                    response.encoding,
                    requested_session_id,
                )
                _dump_hermes_io("response", requested_session_id, response.text)
                response.raise_for_status()
                if use_json_schema and self._json_schema_supported is not False:
                    self._json_schema_supported = True
                data = response.json()
            except httpx.TimeoutException as exc:
                raise TimeoutError(
                    f"Hermes chat request timed out after {request_timeout}s; model={self.model}"
                ) from exc
            except httpx.HTTPStatusError as exc:
                detail = exc.response.text[:500] if exc.response is not None else ""
                raise RuntimeError(f"Hermes chat request failed: {exc.response.status_code if exc.response else 'unknown'} {detail}") from exc
            except httpx.HTTPError as exc:
                raise RuntimeError(f"Hermes chat request failed: {type(exc).__name__}: {exc}") from exc

        choices = data.get("choices") if isinstance(data.get("choices"), list) else []
        choice = choices[0] if choices and isinstance(choices[0], dict) else {}
        message = choice.get("message") if isinstance(choice.get("message"), dict) else {}
        content = message.get("content", "")
        gateway_final_content = str(content)
        session_id = response.headers.get("X-Hermes-Session-Id") or requested_session_id
        recovered_content = None
        recovery_error = None
        if not _matches_json_schema(gateway_final_content, json_schema):
            recovered_content, recovery_error = await self._recover_session_json(
                session_id,
                request_started_at=request_started_at,
                json_schema=json_schema,
            )
        content_text = recovered_content or gateway_final_content
        return {
            "raw_response": data,
            "content": content_text,
            "gateway_final_content": gateway_final_content if recovered_content else None,
            "content_source": "session_assistant_message" if recovered_content else "gateway_final_response",
            "session_recovery_error": recovery_error,
            "artifact": _read_hermes_json_artifact(content_text, request_started_at),
            "hermes_session_id": session_id,
            "structured_output_mode": "json_schema" if use_json_schema and self._json_schema_supported else "json_object",
            "finish_reason": str(choice.get("finish_reason") or "") or None,
            "usage": dict(data.get("usage") or {}) if isinstance(data.get("usage"), dict) else {},
            "content_length": len(content_text),
            "elapsed_ms": round((time.time() - request_started_at) * 1000),
        }

    async def _recover_session_json(
        self,
        session_id: str,
        *,
        request_started_at: float,
        json_schema: dict[str, Any] | None = None,
    ) -> tuple[str | None, str | None]:
        url = f"{self._api_root()}/api/sessions/{quote(session_id, safe='')}/messages"
        try:
            async with httpx.AsyncClient(timeout=min(self.timeout, 15.0)) as client:
                response = await client.get(url, headers=self._headers())
                response.raise_for_status()
                payload = response.json()
        except (httpx.HTTPError, ValueError) as exc:
            return None, f"{type(exc).__name__}: {exc}"

        messages = payload.get("data") if isinstance(payload, dict) else None
        if not isinstance(messages, list):
            return None, "Hermes session messages response has no data list"
        for item in reversed(messages):
            if not isinstance(item, dict) or item.get("role") != "assistant":
                continue
            try:
                timestamp = float(item.get("timestamp") or 0)
            except (TypeError, ValueError):
                timestamp = 0
            if timestamp and timestamp < request_started_at - 5:
                continue
            candidate = str(item.get("content") or "")
            if len(candidate.encode("utf-8")) > settings.hermes_artifact_max_bytes:
                continue
            if _matches_json_schema(candidate, json_schema):
                return candidate, None
        return None, "Hermes session contains no schema-valid assistant JSON"

    def _api_root(self) -> str:
        return self.base_url[:-3] if self.base_url.endswith("/v1") else self.base_url

    @staticmethod
    def _response_format(json_schema: dict[str, Any] | None, schema_name: str | None) -> dict[str, Any]:
        schema = dict(json_schema or {})
        schema.pop("$schema", None)
        schema.pop("$id", None)
        name = re.sub(r"[^A-Za-z0-9_-]+", "_", schema_name or "cineforge_output")[:64] or "cineforge_output"
        return {
            "type": "json_schema",
            "json_schema": {
                "name": name,
                "strict": False,
                "schema": schema,
            },
        }

    def _headers(self) -> dict[str, str]:
        return {
            "Authorization": f"Bearer {self.api_key}",
            "Content-Type": "application/json",
        }


def _safe_json(response: httpx.Response) -> Any:
    try:
        return response.json()
    except ValueError:
        return response.text[:500]


_HERMES_IO_DIR = Path(settings.hermes_io_dump_dir)


def _dump_hermes_io(kind: str, session_id: str, payload: Any) -> None:
    """把完整的请求/响应内容写入独立文件，避免终端截断。

    受 settings.hermes_io_dump_enabled 控制，默认关闭。
    文件路径: {hermes_io_dump_dir}/{timestamp}_{kind}_{session_id}.json
    """
    if not settings.hermes_io_dump_enabled:
        return
    try:
        _HERMES_IO_DIR.mkdir(parents=True, exist_ok=True)
        ts = time.strftime("%Y%m%d_%H%M%S")
        filepath = _HERMES_IO_DIR / f"{ts}_{kind}_{session_id}.json"
        if isinstance(payload, (dict, list)):
            text = json.dumps(payload, ensure_ascii=False, indent=2)
        else:
            text = str(payload)
            # 尝试格式化 JSON 字符串
            try:
                text = json.dumps(json.loads(text), ensure_ascii=False, indent=2)
            except (TypeError, ValueError):
                pass
        filepath.write_text(text, encoding="utf-8")
        logger.info("hermes.chat_json %s 完整内容已写入 %s", kind, filepath)
    except OSError as exc:
        logger.warning("hermes.chat_json 写入 %s 文件失败: %s", kind, exc)


def _pretty_json(text: str) -> str:
    """把可能是 JSON 的文本格式化为多行，便于日志完整查看；非 JSON 原样返回。"""
    try:
        return json.dumps(json.loads(text), ensure_ascii=False, indent=2)
    except (TypeError, ValueError):
        return text


def _json_delivery_system_prompt(system: str) -> str:
    prompt = str(system or "").strip()
    if _JSON_DELIVERY_POLICY in prompt:
        return prompt
    return f"{prompt}\n\n{_JSON_DELIVERY_POLICY}" if prompt else _JSON_DELIVERY_POLICY


def _contains_json_object(content: str) -> bool:
    return _json_object_from_content(content) is not None


def _matches_json_schema(content: str, json_schema: dict[str, Any] | None) -> bool:
    parsed = _json_object_from_content(content)
    if parsed is None:
        return False
    if not json_schema:
        return True
    return not any(Draft202012Validator(json_schema).iter_errors(parsed))


def _json_object_from_content(content: str) -> dict[str, Any] | None:
    text = str(content or "").strip()
    if not text:
        return None
    fence = re.search(r"```(?:json)?\s*(.*?)\s*```", text, flags=re.S)
    if fence:
        text = fence.group(1)
    start = text.find("{")
    end = text.rfind("}")
    if start < 0 or end <= start:
        return None
    try:
        parsed = json.loads(text[start : end + 1])
    except json.JSONDecodeError:
        return None
    return parsed if isinstance(parsed, dict) else None


def _schema_format_rejected(detail: str) -> bool:
    lowered = str(detail or "").lower()
    return any(token in lowered for token in ("response_format", "json_schema", "json schema"))


_HERMES_JSON_ARTIFACT_RE = re.compile(
    r"/opt/data/([A-Za-z0-9][A-Za-z0-9._-]{0,254}\.json)\b"
)


def _read_hermes_json_artifact(content: str, request_started_at: float) -> dict[str, Any] | None:
    matches = _HERMES_JSON_ARTIFACT_RE.findall(content)
    if not matches:
        return None
    base = Path(settings.hermes_artifact_dir).resolve()
    for filename in reversed(matches):
        candidate = base / filename
        try:
            if candidate.is_symlink():
                continue
            resolved = candidate.resolve(strict=True)
            stat = resolved.stat()
        except OSError:
            continue
        if resolved.parent != base or not resolved.is_file():
            continue
        if stat.st_size <= 0 or stat.st_size > settings.hermes_artifact_max_bytes:
            continue
        if stat.st_mtime < request_started_at - 5:
            continue
        try:
            artifact_content = resolved.read_text(encoding="utf-8")
        except (OSError, UnicodeError):
            continue
        return {
            "path": f"/opt/data/{filename}",
            "content": artifact_content,
            "content_length": len(artifact_content),
        }
    return None


hermes_client = HermesClient()
