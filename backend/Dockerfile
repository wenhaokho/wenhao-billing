# syntax=docker/dockerfile:1.7

# ---------- stage 1: build Python deps into a venv ----------
FROM python:3.12-slim AS builder

ENV PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# Build tools for any deps that compile from source (psycopg/bcrypt fallbacks).
# They stay in this stage; the runtime image below never gets them.
RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential libpq-dev \
    && rm -rf /var/lib/apt/lists/*

RUN python -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH

# Same WORKDIR as the runtime stage: the editable install links the package to
# /app, where the source lives (bind-mounted in dev, copied in prod-style builds).
WORKDIR /app
COPY pyproject.toml ./
RUN pip install --upgrade pip \
    && pip install -e ".[dev]"

# ---------- stage 2: runtime ----------
FROM python:3.12-slim AS base

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PATH=/opt/venv/bin:$PATH

WORKDIR /app

# Runtime libpq only — no compilers or headers. Also upgrade the base image's
# system pip, which the scanner flags even though the app runs from the venv.
RUN apt-get update \
    && apt-get install -y --no-install-recommends libpq5 \
    && rm -rf /var/lib/apt/lists/* \
    && /usr/local/bin/python -m pip install --upgrade pip

COPY --from=builder /opt/venv /opt/venv

# Headless Chromium for HTML→PDF invoice/quotation rendering (Playwright).
RUN python -m playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*

# App source is bind-mounted in dev via docker-compose; copy for prod-style builds.
COPY . .

EXPOSE 8000
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
