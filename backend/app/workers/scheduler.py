"""In-process scheduler for the periodic jobs (replaces Celery beat + worker).

Runs inside the API process on a background thread, started/stopped by the
FastAPI lifespan. Jobs are plain sync functions, so they run on the scheduler's
thread pool and never block the event loop.

Each run takes a Postgres advisory lock keyed on the job id, so if the app is
ever started with multiple uvicorn workers or replicas, only one process runs a
given job at a time; the others skip.
"""

from __future__ import annotations

import logging
import zlib
from collections.abc import Callable

from apscheduler.schedulers.background import BackgroundScheduler
from apscheduler.triggers.cron import CronTrigger
from sqlalchemy import text

from app.db.session import engine
from app.workers.tasks.fx_sync import sync_fx_rates
from app.workers.tasks.hosting_enforcement import daily_hosting_enforcement
from app.workers.tasks.recurring import scan_and_generate
from app.workers.tasks.usage_lock import cutoff_scan

log = logging.getLogger(__name__)

# (job id, function, cron trigger) — all times UTC.
JOBS: list[tuple[str, Callable[[], object], CronTrigger]] = [
    ("recurring-scan-and-generate", scan_and_generate, CronTrigger(hour=2, minute=0)),
    ("usage-lock-cutoff-scan", cutoff_scan, CronTrigger(hour=3, minute=0)),
    ("hosting-overdue-enforcement", daily_hosting_enforcement, CronTrigger(hour=4, minute=0)),
    # Monday 05:00 UTC — provider refreshes on working days.
    ("fx-rates-weekly-sync", sync_fx_rates, CronTrigger(day_of_week="mon", hour=5, minute=0)),
]


def _run_locked(job_id: str, func: Callable[[], object]) -> None:
    key = zlib.crc32(job_id.encode())
    with engine.connect() as conn:
        if not conn.scalar(text("SELECT pg_try_advisory_lock(:k)"), {"k": key}):
            log.info("job %s skipped: already running in another process", job_id)
            return
        try:
            result = func()
            log.info("job %s finished: %r", job_id, result)
        except Exception:
            log.exception("job %s failed", job_id)
        finally:
            conn.execute(text("SELECT pg_advisory_unlock(:k)"), {"k": key})


def build_scheduler() -> BackgroundScheduler:
    # Schedules live in memory: a run whose time passes while the app is down
    # (e.g. mid-redeploy) is skipped, same as with the old Celery beat.
    scheduler = BackgroundScheduler(
        timezone="UTC",
        job_defaults={"coalesce": True, "max_instances": 1, "misfire_grace_time": 3600},
    )
    for job_id, func, trigger in JOBS:
        scheduler.add_job(_run_locked, trigger, args=(job_id, func), id=job_id)
    return scheduler
