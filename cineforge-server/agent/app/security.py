from __future__ import annotations

import base64
import hashlib
import hmac
import json
import secrets
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from fastapi import Depends, HTTPException
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.config import settings
from app.db.base import async_session, get_session
from app.models.core import User
from app.models.enums import UserRole

_bearer = HTTPBearer(auto_error=False)


def hash_password(password: str) -> str:
    salt = secrets.token_hex(16)
    digest = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt.encode("utf-8"), 120_000)
    return f"pbkdf2_sha256${salt}${digest.hex()}"


def verify_password(password: str, password_hash: str | None) -> bool:
    if not password_hash:
        return False
    try:
        scheme, salt, expected = password_hash.split("$", 2)
    except ValueError:
        return False
    if scheme != "pbkdf2_sha256":
        return False
    digest = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt.encode("utf-8"), 120_000)
    return hmac.compare_digest(digest.hex(), expected)


def create_access_token(user_id: UUID, role: UserRole, session_expires_at: int) -> str:
    payload = {
        "sub": str(user_id),
        "role": role.value,
        "exp": session_expires_at,
    }
    body = _b64(json.dumps(payload, separators=(",", ":")).encode("utf-8"))
    signature = _sign(body)
    return f"{body}.{signature}"


async def get_current_user(
    credentials: HTTPAuthorizationCredentials | None = Depends(_bearer),
    session: AsyncSession = Depends(get_session),
) -> User:
    if credentials is None:
        raise HTTPException(status_code=401, detail="Not authenticated")
    payload = _decode_token(credentials.credentials)
    user_id = payload.get("sub")
    if not user_id:
        raise HTTPException(status_code=401, detail="Invalid token")
    user = await session.get(User, UUID(str(user_id)))
    if user is None or not user.is_active:
        raise HTTPException(status_code=401, detail="User disabled or not found")
    return user


async def get_current_user_detached(
    credentials: HTTPAuthorizationCredentials | None = Depends(_bearer),
) -> User:
    """Authenticate without retaining a request-scoped DB session for streaming responses."""
    if credentials is None:
        raise HTTPException(status_code=401, detail="Not authenticated")
    payload = _decode_token(credentials.credentials)
    user_id = payload.get("sub")
    if not user_id:
        raise HTTPException(status_code=401, detail="Invalid token")
    async with async_session() as session:
        user = await session.get(User, UUID(str(user_id)))
    if user is None or not user.is_active:
        raise HTTPException(status_code=401, detail="User disabled or not found")
    return user


def require_director_or_admin(user: User = Depends(get_current_user)) -> User:
    if user.role not in {UserRole.director, UserRole.admin}:
        raise HTTPException(status_code=403, detail="Director or admin permission required")
    return user


def is_director_or_admin(user: User) -> bool:
    return user.role in {UserRole.director, UserRole.admin}


def default_password_for_phone(phone: str) -> str:
    return phone[-6:]


def _decode_token(token: str) -> dict[str, Any]:
    try:
        body, signature = token.split(".", 1)
    except ValueError as exc:
        raise HTTPException(status_code=401, detail="Invalid token") from exc
    if not hmac.compare_digest(_sign(body), signature):
        raise HTTPException(status_code=401, detail="Invalid token")
    try:
        payload = json.loads(base64.urlsafe_b64decode(_pad(body)).decode("utf-8"))
    except (ValueError, json.JSONDecodeError) as exc:
        raise HTTPException(status_code=401, detail="Invalid token") from exc
    if int(payload.get("exp", 0)) < int(datetime.now(UTC).timestamp()):
        raise HTTPException(status_code=401, detail="Token expired")
    return payload


def _sign(body: str) -> str:
    secret = settings.auth_secret.encode("utf-8")
    return _b64(hmac.new(secret, body.encode("utf-8"), hashlib.sha256).digest())


def _b64(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


def _pad(value: str) -> bytes:
    return (value + "=" * (-len(value) % 4)).encode("ascii")


async def find_user_by_phone(session: AsyncSession, phone: str) -> User | None:
    result = await session.execute(select(User).where(User.phone == phone))
    return result.scalar_one_or_none()
