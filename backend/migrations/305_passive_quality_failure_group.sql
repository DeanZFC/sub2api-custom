-- Preserve unknown historical membership; never attribute it to today's group.
ALTER TABLE passive_quality_failures ADD COLUMN group_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE passive_quality_failures ADD COLUMN non_text BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX passive_quality_failures_group_window ON passive_quality_failures(account_id,group_id,model,created_at DESC);
