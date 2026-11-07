-- Retire group-level user blocking. Account metric rules have separate scope
-- and remain opt-in; do not translate old group settings into account rules.
DELETE FROM settings WHERE key ~ '^group_anti_distillation:[0-9]+$';
