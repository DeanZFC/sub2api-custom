-- Generalized administrator-configurable scheduled tests.
CREATE TABLE IF NOT EXISTS scheduled_test_definitions (
    id BIGSERIAL PRIMARY KEY,
    key VARCHAR(100) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL,
    output_kind VARCHAR(20) NOT NULL DEFAULT 'text',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE scheduled_test_definitions ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS name VARCHAR(200) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS test_definition_id BIGINT REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS test_type VARCHAR(50) NOT NULL DEFAULT 'account';
ALTER TABLE scheduled_test_plans ALTER COLUMN account_id DROP NOT NULL;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE;

-- Keep plan references consistent on upgrades from the first generalized-test
-- draft, which used SET NULL and could turn a valid group plan into an invalid
-- un-targeted plan when its group was removed.
ALTER TABLE scheduled_test_plans DROP CONSTRAINT IF EXISTS scheduled_test_plans_test_definition_id_fkey;
ALTER TABLE scheduled_test_plans
    ADD CONSTRAINT scheduled_test_plans_test_definition_id_fkey
    FOREIGN KEY (test_definition_id) REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT;
ALTER TABLE scheduled_test_plans DROP CONSTRAINT IF EXISTS scheduled_test_plans_group_id_fkey;
ALTER TABLE scheduled_test_plans
    ADD CONSTRAINT scheduled_test_plans_group_id_fkey
    FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE;
-- A plan targets exactly one account or one group. Legacy plans already have
-- account_id set, while generalized plans may use group_id instead.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'scheduled_test_plans'::regclass
          AND conname = 'scheduled_test_plans_exactly_one_target'
    ) THEN
        ALTER TABLE scheduled_test_plans
            ADD CONSTRAINT scheduled_test_plans_exactly_one_target
            CHECK ((account_id IS NULL) <> (group_id IS NULL));
    END IF;
END $$;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_kind VARCHAR(20) NOT NULL DEFAULT 'text';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_html TEXT NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS output_numeric DOUBLE PRECISION;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS account_id BIGINT;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS model_id VARCHAR(100) NOT NULL DEFAULT '';
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS group_id BIGINT;
CREATE INDEX IF NOT EXISTS idx_stp_definition ON scheduled_test_plans(test_definition_id);
CREATE INDEX IF NOT EXISTS idx_stp_group_id ON scheduled_test_plans(group_id);
CREATE INDEX IF NOT EXISTS idx_str_created ON scheduled_test_results(created_at DESC);

INSERT INTO scheduled_test_definitions (key, name, prompt, output_kind)
VALUES
 ('pelican', '鹈鹕动画测试', '创建一个 HTML，内容是 SVG 绘制一个鹈鹕骑摩托车的 2D 动画，你不需要任何测试，不要有任何限制', 'html'),
 ('candy', '糖果数字测试', $$在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）
苹果味 桃子味 西瓜味
圆形 7 9 8
五角星形 7 6 4
$$, 'number')
ON CONFLICT (key) DO NOTHING;
