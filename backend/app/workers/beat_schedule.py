from celery.schedules import crontab

BEAT_SCHEDULE = {
    "recurring-scan-and-generate": {
        "task": "app.workers.tasks.recurring.scan_and_generate",
        "schedule": crontab(hour=2, minute=0),
    },
    "usage-lock-cutoff-scan": {
        "task": "app.workers.tasks.usage_lock.cutoff_scan",
        "schedule": crontab(hour=3, minute=0),
    },
}
