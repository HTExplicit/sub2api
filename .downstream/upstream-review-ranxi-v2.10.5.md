# ranxi v2.10.5 borrowing review

Historical reference: `d977e7a5e24e5536db6ce8b8478743e0c4b11f77`, released v2.10.5, reviewed over official Wei-Shaw v0.2.15. Borrowing and its reference/probe tooling are retired; current behavior is in [DOWNSTREAM](../DOWNSTREAM.md) and [retired borrowing](native-domains.md#retired-gateway-borrowing).

## Protocol evidence

The historical differential reference pinned source/generated hashes and covered two-shot requests, verdicts, cookie rules and STATE echo. Host-adapter hashes bounded the review; they did not prove a second live service or successful model calls. Astra qualification did not qualify Sol.

## Changes

The scoped update restored native STATE echo, allowed one observation retry only before any request headers were written, and retained completed terminals during SSE keepalive cancellation. Bounded replay accepted no caller credentials, routing cookies, destination override or response anchor. Production proxy selection and live results were separate evidence.

## Excluded release changes

BPS/Redis/tool routing, timezone rewriting, subscription renewal, Grok video, automatic grouping and Mihomo rotation were excluded. No recurring ranxi update was installed.
