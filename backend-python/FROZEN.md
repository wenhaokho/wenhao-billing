# Frozen

This Python backend is frozen while it is replaced by the Go backend in
`backend/` (see `docs/superpowers/specs/2026-11-08-go-backend-design.md`).

- Frozen at commit `2ae13e44af695e625c78fa067fef8e700127724c` (the parent of
  the commit that moved it here from `backend/`).
- Alembic head: `0028_business_npwp`.
- Tests at the freeze: 527 passed (`pytest -q` in the dev stack's `backend`
  service against `billing_test`).

No change to its code, tests, migrations or dependencies until it is deleted
after the Go switch-over. Only its path moved. Behaviour that looks wrong is
recorded in the Go backend's `PORT-NOTES.md`, not fixed here.
