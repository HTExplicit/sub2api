-- Remove the retired remote-rules bootstrap from the stored system prompt library.
--
-- The legacy default rule carried a block that told the model to fetch three
-- files under <host>/skills/security-research/current/ before it worked. That
-- public route belonged to the remote Skill registry, which release #206
-- deleted, and 259 kept the rule's inline text as a library prompt without the
-- registry. The path now falls through to the single-page front end and answers
-- 200 text/html, so a client that follows the instruction reads an HTML page
-- where it was promised Markdown and reports that it cannot read the rules.
--
-- Only that block goes. It is recognised by shape, not by its words: a fenced
-- paragraph that names the removed route, the paragraphs directly after it that
-- still mention REMOTE_ROOT (the file list and the fallback note), and the one
-- paragraph directly before it when that paragraph is the short fetch
-- instruction. A prompt whose route reference sits anywhere else, for example
-- inside a long paragraph, or whose body would be empty without the block, is
-- left as stored and reported with a notice. Every other paragraph, the order
-- of the library, the enabled switch, the default prompt and every other
-- setting are not touched; a library without the route is not written.
--
-- Running the file again is a no-op. A missing, empty or unparsable
-- system_prompts row is left alone.
-- No explicit BEGIN/COMMIT: the runner owns this file's transaction.
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '120s';

DO $strip_remote_bootstrap$
DECLARE
    dead_path CONSTANT TEXT := '/skills/security-research/current';
    stored    TEXT;
    config    JSONB;
    item      JSONB;
    out_items JSONB := '[]'::JSONB;
    body      TEXT;
    new_body  TEXT;
    paras     TEXT[];
    total     INT;
    anchor    INT;
    cut_from  INT;
    cut_to    INT;
    idx       INT;
    changed   BOOLEAN := FALSE;
    remaining INT := 0;
BEGIN
    SELECT value INTO stored FROM settings WHERE key = 'system_prompts';
    IF stored IS NULL OR btrim(stored) = '' THEN
        RETURN;
    END IF;
    BEGIN
        config := stored::JSONB;
    EXCEPTION WHEN others THEN
        RAISE NOTICE '269: system_prompts is not valid JSON, left unchanged';
        RETURN;
    END;
    IF jsonb_typeof(config) IS DISTINCT FROM 'object'
       OR jsonb_typeof(config->'prompts') IS DISTINCT FROM 'array' THEN
        RETURN;
    END IF;

    FOR item IN SELECT elem FROM jsonb_array_elements(config->'prompts') AS elem LOOP
        IF jsonb_typeof(item) = 'object'
           AND jsonb_typeof(item->'body') = 'string'
           AND strpos(item->>'body', dead_path) > 0 THEN
            body := item->>'body';
            LOOP
                paras := string_to_array(body, E'\n\n');
                total := COALESCE(array_length(paras, 1), 0);
                anchor := 0;
                FOR idx IN 1..total LOOP
                    IF strpos(paras[idx], dead_path) > 0 THEN
                        anchor := idx;
                        EXIT;
                    END IF;
                END LOOP;
                EXIT WHEN anchor = 0;

                IF left(paras[anchor], 3) <> '```' OR right(paras[anchor], 3) <> '```'
                   OR length(paras[anchor]) > 500 THEN
                    RAISE NOTICE '269: prompt % keeps a route reference outside a fenced block', item->>'id';
                    EXIT;
                END IF;

                cut_from := anchor;
                IF anchor > 1 AND length(paras[anchor - 1]) <= 400
                   AND paras[anchor - 1] ILIKE '%fetch%' AND paras[anchor - 1] ILIKE '%cloud%' THEN
                    cut_from := anchor - 1;
                END IF;
                cut_to := anchor;
                WHILE cut_to < total
                      AND length(paras[cut_to + 1]) <= 600
                      AND (strpos(paras[cut_to + 1], 'REMOTE_ROOT') > 0
                           OR strpos(paras[cut_to + 1], dead_path) > 0) LOOP
                    cut_to := cut_to + 1;
                END LOOP;

                new_body := array_to_string(paras[1:cut_from - 1] || paras[cut_to + 1:total], E'\n\n');
                IF btrim(new_body) = '' THEN
                    RAISE NOTICE '269: prompt % would be empty without the block, left as stored', item->>'id';
                    EXIT;
                END IF;
                body := new_body;
            END LOOP;

            IF body IS DISTINCT FROM item->>'body' THEN
                item := jsonb_set(item, '{body}', to_jsonb(body));
                changed := TRUE;
            END IF;
            IF strpos(body, dead_path) > 0 THEN
                remaining := remaining + 1;
            END IF;
        END IF;
        out_items := out_items || jsonb_build_array(item);
    END LOOP;

    IF changed THEN
        UPDATE settings
        SET value = jsonb_set(config, '{prompts}', out_items)::TEXT, updated_at = NOW()
        WHERE key = 'system_prompts';
    END IF;
    RAISE NOTICE '269: system_prompts changed=%, prompts still naming the removed route=%', changed, remaining;
END;
$strip_remote_bootstrap$;
