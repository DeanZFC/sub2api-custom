-- A local log snapshot; no upstream prompt or new persistence column is needed.
INSERT INTO scheduled_test_definitions (key, name, description, prompt, output_kind, enabled, sort_order)
VALUES ('hourly_stats', '近一小时统计', '按配置分组、账号和模型统计最近一小时请求成功率、缓存率及平均首字延迟，不请求上游模型。', '', 'statistics', true, 2)
ON CONFLICT (key) DO NOTHING;
