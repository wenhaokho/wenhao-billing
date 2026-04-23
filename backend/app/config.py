from functools import lru_cache

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    database_url: str = Field(
        default="postgresql+psycopg://billing:billing@localhost:5432/billing"
    )
    redis_url: str = Field(default="redis://localhost:6379/0")

    session_secret: str = Field(default="dev-session-secret-change-me")
    session_cookie_name: str = "billing_session"
    session_max_age_seconds: int = 60 * 60 * 8

    webhook_hmac_secret: str = Field(default="dev-webhook-secret-change-me")
    email_webhook_token: str = Field(default="dev-email-token-change-me")

    cors_origins: list[str] = Field(default_factory=lambda: ["http://localhost:5173"])

    # Functional / reporting currency. Every journal line stores a base_amount
    # in this currency so consolidated reports can SUM a single column.
    base_currency: str = Field(default="IDR", min_length=3, max_length=3)

    # SMTP / email. If smtp_host is unset we fall back to logging the message —
    # useful for dev so password-reset flows still work without a real relay.
    smtp_host: str | None = Field(default=None)
    smtp_port: int = Field(default=587)
    smtp_username: str | None = Field(default=None)
    smtp_password: str | None = Field(default=None)
    smtp_use_tls: bool = Field(default=True)
    smtp_from_email: str = Field(default="no-reply@wenhao-billing.local")
    smtp_from_name: str = Field(default="wenhao-billing")

    # Base URL the user's browser uses to reach the frontend. Used to build
    # absolute reset-password links in outgoing emails.
    app_base_url: str = Field(default="http://localhost:5173")


@lru_cache
def get_settings() -> Settings:
    return Settings()
