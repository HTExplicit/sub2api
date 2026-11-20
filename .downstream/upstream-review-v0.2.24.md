# Official v0.2.24 integration

The [risk report](upstream-risk.json) records the official and downstream
comparison inputs. The [sync issue](https://github.com/HTExplicit/sub2api/issues/311)
records the seven text conflicts.

| Surface | Integration decision |
| --- | --- |
| Gateway | Adopt the official inbound field validation, protocol-specific body errors and routing fixes at their existing boundaries. Keep downstream prompt and observation hooks at the final protocol entry points. |
| OAuth | Take the official OpenAI credential compare-and-swap and scheduler outbox update. Retain the existing Grok coverage and PostgreSQL driver value handling in the repository tests. |
| Account administration | Keep the configured admin API fixture and bulk task contracts. Use the official Bedrock payload builder with the administrator's prompt and the existing effort handling. |
| Account usage | Use the official Claude/Gemini refresh key and Claude window estimates in the existing virtualized views and five-minute caches. Close over each request's refresh key so a late response does not replace a newer snapshot. |

The official release adds no SQL migration. Existing migration filenames and
checksums remain applicable.
