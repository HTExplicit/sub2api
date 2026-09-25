-- One capacity snapshot per account: the upstream-native
-- extra.upstream_model_metadata. Every capacity the fork's separate raw
-- snapshot (extra.upstream_model_context_capacities) recorded was declared by
-- the account's own upstream, so it moves into the matching model entry with
-- source "upstream" and its observation time, replacing any capacity fields
-- that entry held (those could only be models.dev enrichment). The raw snapshot
-- and its source-identity hash are then removed. Capability fields and
-- extra.model_context_overrides are untouched.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

WITH legacy AS (
    SELECT a.id,
           a.extra -> 'upstream_model_context_capacities' AS snapshot,
           CASE WHEN jsonb_typeof(a.extra -> 'upstream_model_metadata') = 'object'
                THEN a.extra -> 'upstream_model_metadata' END AS metadata
    FROM accounts a
    WHERE a.extra ? 'upstream_model_context_capacities'
),
observed AS (
    SELECT l.id,
           m.key AS model_id,
           jsonb_strip_nulls(jsonb_build_object(
               'context_window', CASE WHEN jsonb_typeof(m.value -> 'context_window') = 'number' THEN
                   CASE WHEN (m.value ->> 'context_window')::numeric > 0 THEN m.value -> 'context_window' END END,
               'max_context_window', CASE WHEN jsonb_typeof(m.value -> 'max_context_window') = 'number' THEN
                   CASE WHEN (m.value ->> 'max_context_window')::numeric > 0 THEN m.value -> 'max_context_window' END END,
               'max_input_tokens', CASE WHEN jsonb_typeof(m.value -> 'max_input_tokens') = 'number' THEN
                   CASE WHEN (m.value ->> 'max_input_tokens')::numeric > 0 THEN m.value -> 'max_input_tokens' END END,
               'max_output_tokens', CASE WHEN jsonb_typeof(m.value -> 'max_output_tokens') = 'number' THEN
                   CASE WHEN (m.value ->> 'max_output_tokens')::numeric > 0 THEN m.value -> 'max_output_tokens' END END
           )) AS capacity,
           COALESCE(NULLIF(m.value ->> 'observed_at', ''), NULLIF(l.snapshot ->> 'observed_at', '')) AS observed_at
    FROM legacy l
    CROSS JOIN LATERAL jsonb_each(
        CASE WHEN jsonb_typeof(l.snapshot -> 'models') = 'object' THEN l.snapshot -> 'models' ELSE '{}'::jsonb END
    ) AS m
    WHERE jsonb_typeof(m.value) = 'object'
),
entries AS (
    SELECT o.id,
           jsonb_object_agg(
               o.model_id,
               (COALESCE(CASE WHEN jsonb_typeof(l.metadata -> 'models' -> o.model_id) = 'object'
                              THEN l.metadata -> 'models' -> o.model_id END,
                         jsonb_build_object('id', o.model_id))
                   - ARRAY['context_window', 'max_context_window', 'max_input_tokens', 'max_output_tokens', 'source', 'observed_at'])
               || o.capacity
               || jsonb_build_object('source', 'upstream')
               || CASE WHEN o.observed_at IS NULL THEN '{}'::jsonb
                       ELSE jsonb_build_object('observed_at', o.observed_at) END
           ) AS models
    FROM observed o
    JOIN legacy l ON l.id = o.id
    WHERE o.capacity <> '{}'::jsonb
    GROUP BY o.id
)
UPDATE accounts a
SET extra = CASE
        WHEN e.models IS NULL THEN a.extra - 'upstream_model_context_capacities'
        ELSE (a.extra - 'upstream_model_context_capacities') || jsonb_build_object(
            'upstream_model_metadata',
            COALESCE(l.metadata, jsonb_build_object(
                'source', 'upstream',
                'synced_at', COALESCE(NULLIF(l.snapshot ->> 'observed_at', ''), '')
            )) || jsonb_build_object(
                'models',
                COALESCE(CASE WHEN jsonb_typeof(l.metadata -> 'models') = 'object' THEN l.metadata -> 'models' END, '{}'::jsonb)
                    || e.models
            )
        )
    END
FROM legacy l
LEFT JOIN entries e ON e.id = l.id
WHERE a.id = l.id;
