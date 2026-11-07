# Quotation send: delivery contract decision needed

The Go replacement implements `POST /api/v1/quotations/{quotation_id}/send` synchronously using the configured sender. It returns success after provider acceptance and the quotation commit; it serializes concurrent quotation changes with a row lock.

The Python endpoint renders a PDF, calls the mail provider synchronously, then changes a draft to `SENT`, sets `last_sent_at`, commits and returns the quotation. It permits sending again, so repeated requests can be intentional resends. When email is disabled, Python logs the message and still commits a successful send. The quotation detail page displays `last_sent_at` as “Last sent”.

A synchronous Go port can use the injectable `email.Sender` and preserve the timing of a successful response, but it cannot guarantee durable or idempotent delivery. If the provider accepts a message and the process crashes or the database commit fails, no committed state proves delivery. Retrying can send a duplicate. A timeout or connection error can also leave acceptance uncertain; the existing sender interface does not distinguish that case. Holding a quotation row lock through the call prevents simultaneous database transitions but does not solve the provider/commit gap. Treating disabled email as successful would additionally claim a send that did not occur; the Go sender instead reports `ErrDisabled`.

A transactional outbox can atomically persist the message snapshot and a delivery request with the quotation state, but queue acceptance is not mail delivery. Returning the current response and setting `last_sent_at` at enqueue time would mislead the existing UI. An outbox also does not alone ensure exactly-once delivery: SMTP acceptance followed by a crash can lead to a duplicate on retry. The recurring notification worker explicitly uses at-least-once delivery. A request idempotency key is needed to distinguish an HTTP retry from a deliberate resend, and provider-supported idempotency or an explicit manual-review state is needed for uncertain provider outcomes.

The present implementation follows option 1 below. A future durable implementation requires an explicit delivery contract:

1. **Synchronous compatibility:** return success only after provider acceptance and commit. Document the uncertain-outcome and duplicate risks, reject disabled delivery, and add a client idempotency mechanism if desired. This is not durable exactly-once delivery.
2. **Durable delivery:** add a versioned outbox migration and a delivery-status API/UI (queued, delivered, failed and uncertain/manual review), preserve a message and PDF snapshot, accept a client idempotency key, and set `last_sent_at` only after confirmed delivery. Define cancellation and retry policy, including the provider-accepted/commit-failed case. This changes the API and UI contract and requires the currently gated migration upgrade/adoption work before deployment to existing databases.

Both choices require tests with a fake provider, PostgreSQL transactions, concurrent sends and status changes, provider errors and ambiguous outcomes; no live provider is needed. The synchronous route has a Postgres integration test using an injectable fake sender; no live provider is contacted.

The Go route rejects malformed recipient or CC addresses and CR/LF in the subject or attachment filename before delivery, to prevent undeliverable messages and header injection. Python's `to_email` is free text and its CC validator returns 422 for invalid input; Go applies the same validation before looking up the quotation. Go returns 400 for invalid recipient and header values. Other request validation details have not been exhaustively compared.

After provider acceptance, the route uses a bounded detached context to finish recording the send even if the HTTP client disconnects. A process crash, database failure, or uncertain provider response can still leave delivery ambiguous.

Requests without a JSON content type use a deliberately redacted model-validation input to avoid reflecting potentially sensitive message text.
