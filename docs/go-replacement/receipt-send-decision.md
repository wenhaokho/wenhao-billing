# Payment receipt delivery contract

Receipt delivery uses a repeatable-read snapshot and locks the administrator, invoice, and payment during rendering and provider delivery. It accepts any recorded payment status, matching the Python route. The receipt uses payment currency for the received amount and invoice currency for invoice totals and balance. Serialization conflicts before provider delivery are retried at most three times.

The route verifies the recipient and generates the PDF and response before calling the provider. It reports provider failures as 502; a missing sender is a configuration error. Unlike Python's disabled-mail behavior, a disabled Go sender does not report successful delivery. Tests use a fake sender.

Provider acceptance may occur before a timeout or connection failure, or before a database commit failure or process crash. Retrying after such an ambiguous result can send a duplicate. A bounded commit continues after acceptance even when the client disconnects. Exactly-once delivery requires a durable outbox and provider idempotency, which this synchronous endpoint does not provide.
