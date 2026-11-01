import sentry_sdk

from app.main import create_app


def test_create_app_does_not_enable_sentry_under_pytest():
    """The dev container carries a real SENTRY_DSN. Tests that exercise failure
    paths must not report their deliberate errors to the live Sentry project."""
    create_app()
    assert not sentry_sdk.get_client().is_active()
