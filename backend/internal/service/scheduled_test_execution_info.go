package service

import "time"

// Nullable counters distinguish an upstream that omitted usage from a measured zero.
// OutputTokens includes ReasoningTokens; never add reasoning to the total again.
type ScheduledTestTokenUsage struct {
	InputTokens         *int64 `json:"input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
	ReasoningTokens     *int64 `json:"reasoning_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_tokens"`
	CacheCreationTokens *int64 `json:"cache_creation_tokens"`
}
type ScheduledTestRequestAttempt struct {
	ScheduledTestTokenUsage
	StartedAt   time.Time  `json:"started_at"`
	HTTPStatus  int        `json:"http_status,omitempty"`
	FailureKind string     `json:"failure_kind,omitempty"`
	RetryAfter  *time.Time `json:"retry_after,omitempty"`
}
type ScheduledTestExecutionInfo struct {
	ScheduledTestTokenUsage
	Attempts            []ScheduledTestRequestAttempt `json:"attempts"`
	FailureKind         string                        `json:"failure_kind,omitempty"`
	RetryAfter          *time.Time                    `json:"retry_after,omitempty"`
	NextCheckAt         *time.Time                    `json:"next_check_at,omitempty"`
	ConsecutiveFailures int                           `json:"consecutive_failures,omitempty"`
}

func newScheduledTestExecutionInfo() *ScheduledTestExecutionInfo {
	return &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{}}
}
func (info *ScheduledTestExecutionInfo) summarize() {
	sum := func(get func(ScheduledTestRequestAttempt) *int64) *int64 {
		var total int64
		for _, attempt := range info.Attempts {
			v := get(attempt)
			if v == nil {
				return nil
			}
			total += *v
		}
		return &total
	}
	info.InputTokens = sum(func(a ScheduledTestRequestAttempt) *int64 { return a.InputTokens })
	info.OutputTokens = sum(func(a ScheduledTestRequestAttempt) *int64 { return a.OutputTokens })
	info.ReasoningTokens = sum(func(a ScheduledTestRequestAttempt) *int64 { return a.ReasoningTokens })
	info.CacheReadTokens = sum(func(a ScheduledTestRequestAttempt) *int64 { return a.CacheReadTokens })
	info.CacheCreationTokens = sum(func(a ScheduledTestRequestAttempt) *int64 { return a.CacheCreationTokens })
}
func mergeScheduledTestExecution(previous, current *ScheduledTestExecutionInfo) *ScheduledTestExecutionInfo {
	if previous == nil {
		return current
	}
	if current == nil {
		return previous
	}
	merged := *current
	merged.Attempts = append(append([]ScheduledTestRequestAttempt{}, previous.Attempts...), current.Attempts...)
	merged.summarize()
	return &merged
}
