# Invoice send delivery contract

The invoice send endpoint synchronously waits for provider acceptance, then commits the DRAFT or OPEN to SENT transition. Other statuses and invoice dates are unchanged. A row lock serializes invoice transitions and repeatable-read provides a coherent invoice, customer, business, payment account and line-item snapshot. The response and PDF are checked and the status update is made within the uncommitted transaction before delivery. Serialization conflicts at the user or invoice lock are retried up to three times before any provider call.

Unlike Python, which logs and reports success when email is disabled, Go returns a delivery error and rolls back. Provider errors and timeouts also roll back. A timeout or connection error can occur after provider acceptance, and a crash or database error can occur between acceptance and commit. In either case a retry can duplicate an email; synchronous delivery cannot guarantee exactly-once results. The route finishes its commit using a bounded context after provider acceptance if the client disconnects. A durable outbox and explicit delivery states would require a separate product/API decision.

Go rejects invalid recipient addresses and CR/LF in subjects and attachment filenames before delivery. CC validation follows the existing quotation route. Tests use fake providers; no live email is sent.
