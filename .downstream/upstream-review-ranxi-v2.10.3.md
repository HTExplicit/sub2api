# ranxi v2.10.3 scoped review

Reference: `fd1b5ee4eeb20961fbb783fa6f136a1704271e90`, released tag v2.10.3.
Official Wei-Shaw v0.2.15 is already integrated; do not merge it again.

| Upstream change | Disposition |
| --- | --- |
| Borrow fingerprint, candidate, STATE probe and automatic preparation core | No change from v2.10.2; correct downstream deviations against the non-rotation branch. |
| Official v0.2.15 protocol compatibility, provider catalog and model directory | Reuse integrated code. `model_protocol_catalog.go` and the single-manifest preservation in `openai_codex_models_pinned.go` are already present. |
| Pelican showcase notice (#356) | Apply its factual interpretation to the downstream workbench: visual works are observations, not model identity or intelligence scores. |
| Excel/BPS OAuth, dynamic tools and model discovery | Excluded. |
| Worker/TOTP, 2FA, tickets, notifications, automatic group takeover, exit rotation | Excluded. |
| Unreleased production branch | Excluded; not the update baseline. |

## Host adaptations retained

- Parameterize target model for Sol, with qualification isolated from Astra.
- Use the host credential/identity builders and currently bound account proxy.
- Keep request-body model context so strict borrowing also applies to large business requests.
- Shared flights, per-waiter cancellation and revision cancellation replace the original global try-lock.
- Serialize complete two-shot probes by account within the existing execution capacity.
- Bounded save preparation continues after individual target failure and checks actual expiry at completion.
- Observation context prevents health, scheduling, quota and billing writes, while allowing normal credential refresh.
- Retain strict dispatch proofs, continuation isolation, diagnostic budget and raw administrator evidence.

## Removed deviations

- Ignoring business STATE in the v12 cache identity.
- Extra cache keys and business-tier injection into the fixed target probe.
- Target rejection forcing a fresh source candidate and retry in the non-rotation path.
- Separate source/target HTTP manager configuration copies and custom TLS policy.

Source alignment and offline regression do not prove successful live borrowing.
Production and live acceptance are recorded separately in the deployment evidence.
