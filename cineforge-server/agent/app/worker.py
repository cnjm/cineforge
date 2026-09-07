from __future__ import annotations

from celery import Celery

from app.config import settings


celery_app = Celery(
    "cineforge",
    broker=settings.redis_url,
    backend=settings.redis_url,
    include=["app.tasks"],
)

celery_app.conf.update(
    task_track_started=True,
    task_time_limit=30 * 60,
    task_soft_time_limit=30 * 60,
    task_acks_late=True,
    worker_prefetch_multiplier=1,
    task_default_queue="cineforge",
)
