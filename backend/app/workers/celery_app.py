from celery import Celery

from app.config import get_settings
from app.workers.beat_schedule import BEAT_SCHEDULE

_settings = get_settings()

celery_app = Celery(
    "wenhao_billing",
    broker=_settings.redis_url,
    backend=_settings.redis_url,
    include=[
        "app.workers.tasks.recurring",
        "app.workers.tasks.usage_lock",
        "app.workers.tasks.recon_async",
    ],
)

celery_app.conf.update(
    task_acks_late=True,
    task_reject_on_worker_lost=True,
    worker_prefetch_multiplier=1,
    timezone="UTC",
    beat_schedule=BEAT_SCHEDULE,
)
