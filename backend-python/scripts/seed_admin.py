"""Create or update an admin user. Run once after `alembic upgrade head`.

Usage:
    python -m scripts.seed_admin admin@example.com "your-password"
"""

from __future__ import annotations

import sys

from passlib.context import CryptContext
from sqlalchemy import select

from app.db.session import SessionLocal
from app.models.user import User

_pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")


def main(email: str, password: str) -> None:
    with SessionLocal() as db:
        user = db.scalar(select(User).where(User.email == email))
        if user is None:
            user = User(email=email, password_hash=_pwd.hash(password), role="admin")
            db.add(user)
            print(f"created admin {email}")
        else:
            user.password_hash = _pwd.hash(password)
            user.role = "admin"
            print(f"updated admin {email}")
        db.commit()


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(1)
    main(sys.argv[1], sys.argv[2])
