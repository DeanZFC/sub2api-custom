INSERT INTO scheduled_test_definitions (key, name, description, prompt, output_kind, enabled, sort_order)
VALUES ('model_check', '返回模型一致性', '比对实际发送给上游的模型与响应元数据中的模型标识，不使用模型自述判断。未返回模型标识时显示无法确认。', 'Please reply with OK.', 'model_check', true, 3)
ON CONFLICT (key) DO NOTHING;
