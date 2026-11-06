# Task 54 report

Implemented `POST /api/v1/invoices/milestone` using a dedicated transactional insert, customer and project row locks, admin recheck, withholding defaults and the shared invoice response serializer. It leaves number, subtotal and lines unset, and records the milestone reference. It produces no ledger entries.

Evidence: a Python FastAPI HTTP oracle and Go HTTP handler run against separately migrated temporary PostgreSQL databases. The differential cases cover explicit/all applicable fields, null/default issue date, negative due days, IDR/USD/SGD gross-up and non-gross withholding, default and no withholding rates, precision, invalid bodies, missing customer and project and rollback. Database tests cover eight simultaneous creations, no line or journal writes, foreign-key and invalid-response rollback, no-store, demotion including a blocked transaction race, malformed JSON, non-object and wrong media, body limits, unauthenticated access, customer default locking, and OpenAPI properties, responses and security. Authentication, gross-up, and response-rollback guard mutations were each observed failing their respective tests, then restored. SQL was generated with sqlc v1.29.0.

Limits: the differential matrix is representative rather than exhaustive; extreme due-day coercions and every possible date representation are not enumerated. Python returns a plain 500 body on database errors; the test compares status and redacts it to Go's safe generic response.

Final verification: the brief's exact `go test -count=1 ./...` command passed all packages (platform 216.235s, migrations 20.184s) after restoring the mutations and applying the date-validation fix. Focused milestone tests also passed.

Review follow-up: added `exclusiveMinimum: 0` to the numeric amount schema and a direct OpenAPI assertion. The assertion failed before the fix and passed afterward; this red run also verifies the schema guard. Focused milestone tests and the full requested suite passed after the change (platform 220.393s, migrations 19.236s).
