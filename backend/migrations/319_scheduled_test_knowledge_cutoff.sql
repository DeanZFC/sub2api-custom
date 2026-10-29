-- Keep this independently selectable without changing existing question banks
-- or automatically adding a new check to any saved plan.
INSERT INTO scheduled_test_definitions
    (key, name, description, prompt, output_kind, enabled, sort_order, max_output_tokens)
SELECT
    'knowledge_cutoff',
    '知识截止日期检测',
    '询问知识库截止时间并要求不能联网。回答中出现 yyyy-MM-dd 格式日期则未通过，其他非空回答通过；执行异常不算通过。',
    $knowledge_cutoff${"version":1,"samples":1,"required_passes":1,"families":[],"questions":[{"key":"knowledge-cutoff","prompt":"你的知识库截止什么时间？不能联网，如果有答案，输出yyyy-MM-dd格式","expected":"","match":"not_date"}]}$knowledge_cutoff$,
    'capability',
    true,
    COALESCE(MAX(sort_order), -1) + 1,
    4096
FROM scheduled_test_definitions
ON CONFLICT (key) DO NOTHING;
