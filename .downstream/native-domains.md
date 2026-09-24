# Native domains

Seven first-party domains run inside the host: Codex runtime, model policy,
prompt/skills, account tools, Cindy, image tools and observability. Native pages
retain the flat theme, MiSans, account fields and persisted task interactions.
The official third-party plugin framework and its own configuration UI remain.

## Settings and operations

All paths below are relative to `/api/v1`. Settings writes retain administrator
authentication and the existing step-up policy.

| Interface | Contract |
| --- | --- |
| `GET/PUT /admin/settings/codex-runtime` | Original Codex configuration JSON, without an additional response envelope; `Cache-Control: no-store`. The source is encrypted at rest. |
| `GET/PUT /admin/settings/cindy-provider` | Balance, catalog and search switches; the catalog is available at the `/catalog` suffix. |
| `GET/PUT /admin/settings/image-tools` | Image Studio and Responses image bridge switches. |
| `GET/PUT /admin/settings/observability` | Telemetry and native theme switches. |
| `POST /admin/accounts/:id/codex-tickets/stop-job` | Persisted single-account stop operation; the batch endpoint is `/admin/accounts/codex-tickets/batch-stop`. Both return HTTP 202 and retain per-account/model retry. |

The older synchronous ticket-stop endpoint remains compatible. Ordinary account
bulk editing remains HTTP 202; this change does not replace background tasks with
synchronous editing. Existing API-key reveal and account-field protections remain.

## Startup and retained data

Before loading native settings or starting either runtime, one transaction:

1. Validates the saved first-party capability scopes and decrypts configuration.
2. Imports the effective image, observability and Cindy switches only when their
   native settings keys are absent. Existing keys are never overwritten.
3. Saves original installation/binding/bootstrap intent under
   `deplugin_retired_plugins`, disables the seven installations and their bindings,
   and marks their bootstrap records removed.
4. Advances the existing Codex `runtime_generation` once. A fresh database instead
   receives a disabled state anchor with generation 1 and no executable/artifact.

Missing settings may use deployment defaults. Database errors, malformed saved
settings and unknown/non-equivalent capability scopes stop startup; they never
silently enable features. A disabled whole Codex installation is not equivalent
to disabling only route acquisition and therefore requires an explicit migration
decision rather than automatic activation.

The original installation rows, encrypted configuration, artifacts, state and
lease tables remain. The upstream plugin manager cannot list, modify, delete or
replace the retired first-party keys. Native state access uses the original
`codexrip.codex-runtime` owner, namespaces, state keys, CAS and lease generations.
Closed quality-run records and their budgets are not rewritten.

An unchanged normalized configuration keeps its epoch and generation. A changed
Codex configuration is validated, the old epoch is drained, and encrypted source,
configuration hash/version and generation are committed together. Old qualified
connections cannot be reused after an epoch or connection-lease mismatch.

Migration `254_plugin_job_action_digest.sql` preserves full 64-character historical
task action digests. History and retries continue to use saved targets; unsupported
legacy operations fail without choosing other accounts or models.

## Release and rollback boundary

The host is delivered as one immutable OCI image with image provenance. Normal
deployment uses the existing fixed-image `deploy-preserve` flow. It does not add
backups, canaries, model calls or automatic rollback.

`rollback-preserve` still requires a verified compatible data contract. The
deployer recognizes the old and native prompt/skill directory locations by the
same content hashes; missing or ambiguous trees and changed schema remain errors.

Returning to a plugin-based host additionally needs an explicit restoration of
legacy activation/configuration intent before starting that host. The retained
receipt and artifacts supply the original data; **never restore the receipt's old
runtime generation or delete its native anchor**. A plain image change does not
perform this reverse conversion. Such a legacy-host rollback has not been run as
part of the native migration and is not an automatic failure path.

## Verification

Normal required PR checks validate the native domain packages, account scope,
configuration/lease/ledger compatibility and frontend behavior. The release
reuses that source validation. Health endpoints establish availability only;
actual model-quality diagnostics remain separately budgeted and coordinated.
