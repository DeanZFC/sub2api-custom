-- Add the newly published GPT-6 Astra model to the factory channel-monitor
-- OpenAI allow-list without changing operators' custom or empty-list (show all
-- models) configuration. The @> guard matches the complete factory list from
-- migration 197 and makes this safe to run repeatedly.
UPDATE channel_monitor_v2_config
SET
    platforms = (
        SELECT jsonb_agg(
            CASE
                WHEN elem->>'platform' = 'openai'
                    AND jsonb_typeof(elem->'models') = 'array'
                    AND elem->'models' <> '[]'::jsonb
                    AND elem->'models' @> '["gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-5.6","gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex-spark","gpt-5.2","gpt-5.2-pro","gpt-5","gpt-4.1","gpt-4.1-mini","gpt-4.1-nano","gpt-4o","gpt-4o-mini","gpt-4-turbo","gpt-4","o3","o3-mini","o4-mini","codex-auto-review","gpt-image-2","gpt-image-1"]'::jsonb
                    AND NOT (elem->'models' ? 'gpt-6-astra')
                THEN jsonb_set(elem, '{models}', elem->'models' || '["gpt-6-astra"]'::jsonb)
                ELSE elem
            END
            ORDER BY ord
        )
        FROM jsonb_array_elements(platforms) WITH ORDINALITY AS items(elem, ord)
    ),
    version = version + 1,
    updated_at = NOW()
WHERE id = 1
  AND EXISTS (
      SELECT 1
      FROM jsonb_array_elements(platforms) AS items(elem)
      WHERE elem->>'platform' = 'openai'
        AND jsonb_typeof(elem->'models') = 'array'
        AND elem->'models' <> '[]'::jsonb
        AND elem->'models' @> '["gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-5.6","gpt-5.5","gpt-5.4","gpt-5.4-mini","gpt-5.3-codex-spark","gpt-5.2","gpt-5.2-pro","gpt-5","gpt-4.1","gpt-4.1-mini","gpt-4.1-nano","gpt-4o","gpt-4o-mini","gpt-4-turbo","gpt-4","o3","o3-mini","o4-mini","codex-auto-review","gpt-image-2","gpt-image-1"]'::jsonb
        AND NOT (elem->'models' ? 'gpt-6-astra')
  );
