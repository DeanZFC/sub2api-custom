-- Persist the group that actually accepted an asynchronous batch-image job.
-- The field is nullable for jobs created before fallback-aware routing; new jobs
-- always store the primary group or the selected API-key fallback group.
ALTER TABLE batch_image_jobs
    ADD COLUMN IF NOT EXISTS routed_group_id BIGINT;

CREATE INDEX IF NOT EXISTS batch_image_jobs_routed_group_id_idx
    ON batch_image_jobs (routed_group_id);

COMMENT ON COLUMN batch_image_jobs.routed_group_id IS
    '实际处理并计费的分组；异步批量图片任务使用，旧任务可为空';
