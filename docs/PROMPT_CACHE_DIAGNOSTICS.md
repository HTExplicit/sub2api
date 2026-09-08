# Temporary Responses cache diagnostics

Admin-only Ops endpoints enroll one API key, UUID session header and exact GPT model:

- `POST /api/v1/admin/ops/prompt-cache-diagnostics` with `api_key_id`, `session_id`, `model`, `ttl_seconds` (60–3600; default 1800), and `max_requests` (2–64; default 32).
- `GET /api/v1/admin/ops/prompt-cache-diagnostics/:id` reads an independent snapshot.
- `DELETE /api/v1/admin/ops/prompt-cache-diagnostics/:id` stops enrollment but retains results until expiry plus one hour. Stopping does not interrupt an enrolled inference.

The existing admin middleware and Ops monitoring switch apply. No UI, database migration, account setting, extra inference, request mutation or retry is introduced. Captures are process-local and disappear on restart; no job is active by default. An active duplicate scope returns the existing job. At most four jobs, four in-flight captures globally and eight HTTP attempts per selected request are retained. Requests over 8 MiB are skipped; JSON depth, node and input-item limits mark snapshots incomplete instead of changing inference.

Per-job random HMAC keys are never returned. Records contain field/header/route hashes, input counts and first changed input component, not plaintext prompts, tool output, encrypted reasoning, credentials or URLs. Type/role labels are allowlisted; unknown extension names are hashed together. Safe request UUIDs allow correlation with usage rows. HMAC values are comparable only inside the same diagnostic job.

The ingress snapshot precedes handler normalization. HTTP snapshots cover native OpenAI Responses forwarding and HTTP passthrough, after endpoint adaptation and signature-recovery preparation. A snapshot uses the preparer's exact final bytes or a separate `GetBody` clone; the live body, headers and replay eligibility are not changed. Unsupported paths, omitted attempts and incomplete snapshots are not proof of request equivalence.

`previous_ingress` and `previous_wire` identify their baseline request sequence. Append-only input, equal cache key and unchanged cache-relevant envelope rule out those observed changes, but cannot reveal a provider's hidden routing or eviction. Header/route hashes stop at the gateway's upstream boundary. Raw upstream usage records distinguish an explicitly reported zero cache read from an absent field. Original client cancellation, HTTP headers, first parsed upstream event and completion times help distinguish separate inbound resubmissions from gateway attempts; they do not independently prove a Cloudflare 524 or physical cache eviction.
