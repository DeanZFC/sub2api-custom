-- True request-turn start is frozen before forwarding and survives asynchronous
-- usage persistence. Existing rows deliberately remain unknown, never inferred.
ALTER TABLE passive_quality_cache_reports ADD COLUMN started_at TIMESTAMPTZ;

-- One independent, installation-wide scoring standard. This adds no routing
-- hold and deliberately has no foreign key to a protection policy.
CREATE TABLE channel_quality_score_config (
 id SMALLINT PRIMARY KEY CHECK (id=1),
 config JSONB NOT NULL,
 version BIGINT NOT NULL DEFAULT 1,
 -- Old installations could disable failure capture while policies were off.
 -- Only traffic collected after installing this schema is trusted by default.
 collect_since TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO channel_quality_score_config(id,config) VALUES(1,'{
 "enabled":true,"window_seconds":900,"max_requests":100,"min_samples":20,
 "error_rate":{"weight":40,"good":0,"bad":20},
 "cache_rate":{"weight":25,"good":80,"bad":0},
 "ttft":{"weight":25,"good":2000,"bad":15000},
 "duration":{"weight":10,"good":10000,"bad":120000}
}'::jsonb);
