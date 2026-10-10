# ranxi v2.10.5 borrowing review

Reference: `d977e7a5e24e5536db6ce8b8478743e0c4b11f77`, released v2.10.5.
Official Wei-Shaw v0.2.15 remains the downstream base. This is a scoped update,
not a merge of ranxi's database, BPS, scheduling, or exit-management features.

## Protocol evidence

`.github/scripts/generate_ranxi_borrow_reference.py` extracts original functions
from the pinned Git object, prefixes test-only identifiers, and records source
and generated hashes in `ranxi-borrow-reference.json`. It does not translate the
reference algorithm. `--check` verifies provenance offline when that Git object
is available. The generated Go file is test-only and needs no network in CI.

The differential test covers the original source payload, two-shot target
requests and verdicts, cookie scope/deletion/expiry, and business STATE echo.
Host-dependent preparation, fingerprint and cache policies have source hashes
and retain the previously reviewed account coordination and observation adapters.
These hashes are a review boundary, not a claim that those whole subsystems were
executed as a second reference service.

The source/pool/probe core is unchanged from v2.10.3. Sol remains an independent
downstream extension; an Astra result never qualifies Sol.

## Changes

- Restore original native HTTP business STATE echo behavior: unknown or expired local
  provenance is preserved; a live record identifying a different account still
  strips it. Target two-shot qualification and WS provenance are unchanged.
- Observe connect/TLS/write/header phases. Retry an observation once only when
  transport trace proves failure before any HTTP request headers were written.
  No trace, any response, cancellation, exhausted diagnostic budget, or written
  headers forbids replay. Every connection attempt counts against the budget.
- Port account-test SSE keepalive from #350, preserving the host parser's error
  wording, observation cancellation and already committed successful terminals.
  Body-close cancellation after completion must not erase a completed test.
  A heartbeat is not model output.
- Extend the existing administrator diagnostic with an explicitly selected,
  bounded native replay for comparable fixture/tool sessions. Ordinary source
  controls are limited to saved source accounts. No credentials, routing cookies,
  destination override or previous-response anchor can be supplied by replay.

The production account-proxy selection and live model results are deployment
evidence, not protocol unit-test results. No recurring ranxi update is installed.

## Excluded release changes

BPS credentials/Redis/tool routing, optional timezone rewriting, subscription
renewal, Grok video, automatic grouping and Mihomo rotation are outside this fix.
The existing WS switch and official upstream synchronization remain unchanged.
