"""Shared email-address field types for request schemas."""

import re
from typing import Annotated

from pydantic import AfterValidator, EmailStr, TypeAdapter

_email = TypeAdapter(EmailStr)


def split_emails(value: str | list[str] | None) -> list[str]:
    """Split a comma/semicolon/whitespace-separated address list, dropping blanks."""
    if not value:
        return []
    parts = value if isinstance(value, list) else re.split(r"[,;\s]+", value)
    return [p.strip() for p in parts if p and p.strip()]


def _validate_list(value: list[str]) -> list[str]:
    seen: list[str] = []
    for addr in value:
        try:
            _email.validate_python(addr)
        except ValueError:
            raise ValueError(f"invalid email address: {addr}") from None
        if addr.lower() not in (s.lower() for s in seen):
            seen.append(addr)
    return seen


def _validate_cc(value: str | None) -> str | None:
    addrs = _validate_list(split_emails(value))
    return ", ".join(addrs) or None


# Free-text CC on send requests: one or more addresses separated by commas,
# normalised to "a@x.com, b@y.com" (or None when blank).
CcEmails = Annotated[str | None, AfterValidator(_validate_cc)]

# Stored list of CC addresses (e.g. a customer's default CC recipients).
EmailList = Annotated[list[str], AfterValidator(lambda v: _validate_list(split_emails(v)))]
