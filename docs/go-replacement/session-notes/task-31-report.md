# Task 31: FX reference-data reads

Implemented authenticated Go GET routes for base currency, FX list, and latest stored FX row. Generated sqlc queries use the global `fx_rates` table, descending date/ID ordering, optional filters and cutoff. Decimal values serialize to eight places; timestamps retain Python's microsecond formatting. Nonfinite stored numeric values produce a typed internal error and sanitized HTTP 500. Base currency comes from `BASE_CURRENCY`, defaults to IDR, preserves case for its endpoint, and is uppercased for rate lookup. Explicitly empty and non-three-character configured values are rejected.

## Evidence

- `TestFXReadRequiresAuthentication` ran before implementation and failed with 404 for each missing route; it now passes.
- `TestFXReadsDifferential` seeds separate disposable Postgres databases and compares actual Go HTTP status/JSON against Python FastAPI TestClient HTTP, using real signed sessions and persisted users on both sides. It covers missing/tampered/non-admin/deleted sessions, list, pair and repeated filters, lowercase normalization, missing/short input, default base, latest stored identity, missing rate, inclusive date boundaries, ordering and same-date rows, Unix seconds/milliseconds, invalid short/day/month/year/separator dates, numeric NaN, invalid hour/minute, and midnight/non-midnight datetime. It compares full fields, 8-place decimals, IDs and timestamps including fractional microseconds. Full table snapshots before and after prove reads did not modify either database. Go checks additionally cover no-store, configured lowercase base and NaN safety.
- `TestFXReadOpenAPI` checks all three paths, security, status/error schema references, parameter names/required status/length/date format, and FX response field types, required fields and date formats. `TestFXBaseCurrencyConfiguration` checks accepted and rejected configuration lengths.
- Independently mutated authentication, list currency filter, cutoff comparator, ordering direction, and latest pair selection; each targeted test failed. Originals restored and sqlc regenerated.
- Focused `go test ./internal/platform -run '^TestFXRead|^TestFXBaseCurrency' -count=1` passed after restoration.
- The requested full suite passed on the final code (`internal/platform` 148.212s; all packages passed).

## Limits

The Python date parser supports more input forms and emits more detailed diagnostics than this implementation. The differential covers representative dates and validation messages, not the complete Pydantic/speedate input space. In particular some malformed datetime components and exceptional numeric values may differ. OpenAPI tests do not exhaustively compare every schema detail against Python. No live provider, email, production service, or deployment was used.

## Timezone follow-up

A real HTTP differential against Python, using the same separate disposable Postgres databases and full before/after snapshots, first reproduced incorrect rejection of compact offsets such as `+0530` and incorrect acceptance of `+24:00` and `+05:60`. The Go parser now normalizes compact offsets, accepts upper/lowercase `Z`, and validates offset hours and minutes before parsing. The expanded comparison covers positive and negative compact/colon forms, `23:59` boundaries, hours at or above 24, minutes at or above 60, malformed and trailing characters, exact midnight and non-midnight datetimes, and fractional seconds. It compares status and the full JSON response/error. The focused FX tests pass and both database snapshots remain unchanged.

Five independent mutations—removing compact-offset normalization, relaxing the hour bound, relaxing the minute bound, removing lowercase `z` normalization, and removing the omitted-seconds guard—each caused the differential to fail; all were restored. Omitted-seconds cases also verify that timezone digits are not interpreted as seconds. The requested full suite passed after restoration (all packages green; platform 175.386s). The existing limitation on exhaustive Pydantic date parser parity still applies.
