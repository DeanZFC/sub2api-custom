package service

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This checks the requested shape, not whether the date exists on a calendar.
var qualityCapabilityDatePattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}`)

// A capability definition stores its versioned question-bank configuration in
// the existing prompt column. The UI edits structured fields, not JSON text.
type QualityCapabilityConfig struct {
	Version        int                         `json:"version"`
	Samples        int                         `json:"samples"`
	RequiredPasses int                         `json:"required_passes"`
	Families       []string                    `json:"families"`
	Questions      []QualityCapabilityQuestion `json:"questions,omitempty"`
}
type QualityCapabilityQuestion struct {
	Key      string `json:"key"`
	Prompt   string `json:"prompt"`
	Expected string `json:"expected"`
	Match    string `json:"match"`
}
type QualityCapabilityAnswer struct {
	QualityCapabilityQuestion
	Answer  string `json:"answer"`
	Verdict string `json:"verdict"`
}
type QualityCapabilityResult struct {
	Version        int                       `json:"version"`
	Seed           string                    `json:"seed"`
	Samples        int                       `json:"samples"`
	RequiredPasses int                       `json:"required_passes"`
	Passed         int                       `json:"passed"`
	Verdict        string                    `json:"verdict"`
	Answers        []QualityCapabilityAnswer `json:"answers"`
}

func ParseQualityCapabilityConfig(raw string) (QualityCapabilityConfig, error) {
	c := QualityCapabilityConfig{}
	if len(raw) > 300000 {
		return c, fmt.Errorf("question bank is too large")
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("invalid capability question bank")
	}
	if c.Version != 1 || c.Samples < 1 || c.Samples > 5 || c.RequiredPasses < 1 || c.RequiredPasses > c.Samples {
		return c, fmt.Errorf("capability checks require version 1, 1..5 questions and a valid pass threshold")
	}
	seen := map[string]bool{}
	for _, family := range c.Families {
		if (family != "candy" && family != "state" && family != "instructions") || seen[family] {
			return c, fmt.Errorf("invalid or repeated capability family")
		}
		seen[family] = true
	}
	if len(c.Questions) > 50 {
		return c, fmt.Errorf("at most 50 custom questions")
	}
	seen = map[string]bool{}
	for i := range c.Questions {
		q := &c.Questions[i]
		q.Key, q.Prompt, q.Expected = strings.TrimSpace(q.Key), strings.TrimSpace(q.Prompt), strings.TrimSpace(q.Expected)
		if q.Key == "" || len(q.Key) > 100 || seen[q.Key] || q.Prompt == "" || len(q.Prompt) > 8000 || (q.Expected == "" && q.Match != "not_date") || len(q.Expected) > 2000 || (q.Match != "exact" && q.Match != "number" && q.Match != "not_date") {
			return c, fmt.Errorf("invalid custom question; use a unique key, prompt and exact/number/not_date comparison; exact/number require an answer")
		}
		seen[q.Key] = true
		if q.Match == "number" {
			if _, err := strconv.ParseInt(q.Expected, 10, 64); err != nil {
				return c, fmt.Errorf("numeric reference answers must be integers")
			}
		}
	}
	if len(c.Families) == 0 && len(c.Questions) < c.Samples {
		return c, fmt.Errorf("select generated families or enough distinct custom questions")
	}
	return c, nil
}

func qualityCapabilityQuestions(c QualityCapabilityConfig, seed int64) []QualityCapabilityQuestion {
	rng := mathrand.New(mathrand.NewSource(seed))
	pool := append([]QualityCapabilityQuestion{}, c.Questions...)
	// Rotate all selected families before reusing one. Parameters and reference
	// answers are generated together; no model judges its own response.
	families := append([]string{}, c.Families...)
	rng.Shuffle(len(families), func(i, j int) { families[i], families[j] = families[j], families[i] })
	for i := 0; len(families) > 0 && i < c.Samples; i++ {
		family := families[i%len(families)]
		a, b, d := rng.Intn(30)+5, rng.Intn(7)+2, rng.Intn(20)+3
		q := QualityCapabilityQuestion{Key: fmt.Sprintf("%s-%x-%d", family, uint64(seed), i), Match: "exact"}
		switch family {
		case "candy":
			// Inverse accounting with integer intermediate values.
			start := a * b * 3
			final := (start-start/b)/3 + d
			q.Prompt = fmt.Sprintf("一袋糖先拿走总数的 1/%d，再把剩余糖平均分给三人。小林拿到其中一份后又得到 %d 颗，现在有 %d 颗。最初袋中有多少颗糖？只输出一个十进制整数，不要解释。", b, d, final)
			q.Expected = strconv.Itoa(start)
		case "state":
			x, y := a, d
			steps := make([]string, 0, 8)
			for j := 0; j < 8; j++ {
				switch rng.Intn(3) {
				case 0:
					x, y = y, x
					steps = append(steps, "交换 x 与 y")
				case 1:
					x += y
					steps = append(steps, "x = x + y")
				case 2:
					y = x - y
					steps = append(steps, "y = x - y")
				}
			}
			q.Prompt = fmt.Sprintf("初始 x=%d，y=%d。依次执行以下操作，每步立即更新变量：%s。最终 x-y 等于多少？只输出一个十进制整数，不要解释。", a, d, strings.Join(steps, "；"))
			q.Expected = strconv.Itoa(x - y)
		case "instructions":
			values := []int{a, b, d, a + b, b + d, a + d}
			selected := []string{}
			for j := len(values) - 1; j >= 0; j-- {
				if values[j]%2 == 0 {
					selected = append(selected, strconv.Itoa(values[j]*3))
				}
			}
			q.Expected = strings.Join(selected, ",")
			if q.Expected == "" {
				q.Expected = "EMPTY"
			}
			q.Prompt = fmt.Sprintf("处理序列 %v：保留偶数，保留重复值，将保留项的顺序反转，再将每项乘以 3。仅输出英文逗号分隔的结果，不加空格或括号；没有保留项时仅输出 EMPTY。", values)
		}
		pool = append(pool, q)
	}
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	return pool[:c.Samples]
}

func qualityCapabilityAnswerMatches(q QualityCapabilityQuestion, answer string) bool {
	answer = strings.TrimSpace(answer)
	if q.Match == "not_date" {
		return answer != "" && !qualityCapabilityDatePattern.MatchString(answer)
	}
	if q.Match == "number" {
		actual, err := strconv.ParseInt(answer, 10, 64)
		expected, e := strconv.ParseInt(q.Expected, 10, 64)
		return err == nil && e == nil && actual == expected
	}
	return answer == q.Expected
}

func (s *ScheduledTestRunnerService) runCapabilitySuite(ctx context.Context, plan *ScheduledTestPlan, accountID int64, raw string, execute func(context.Context, string) *ScheduledTestResult) *ScheduledTestResult {
	started := time.Now()
	c, err := ParseQualityCapabilityConfig(raw)
	if err != nil {
		return scheduledTestFailedAttempt(started, err)
	}
	var random [8]byte
	if _, err = rand.Read(random[:]); err != nil {
		return scheduledTestFailedAttempt(started, err)
	}
	seed := int64(binary.LittleEndian.Uint64(random[:]))
	suite := &QualityCapabilityResult{Version: 1, Seed: fmt.Sprintf("%016x", uint64(seed)), Samples: c.Samples, RequiredPasses: c.RequiredPasses, Verdict: "pending", Answers: []QualityCapabilityAnswer{}}
	result := &ScheduledTestResult{Status: "success", StartedAt: started, ExecutionInfo: newScheduledTestExecutionInfo()}
	for _, q := range qualityCapabilityQuestions(c, seed) {
		attempt := s.runWithAutomaticRetries(ctx, plan, &accountID, func(attemptCtx context.Context) *ScheduledTestResult { return execute(attemptCtx, q.Prompt) })
		result.ExecutionInfo = mergeScheduledTestExecution(result.ExecutionInfo, attempt.ExecutionInfo)
		a := QualityCapabilityAnswer{QualityCapabilityQuestion: q, Answer: attempt.ResponseText, Verdict: "fail"}
		if q.Match == "not_date" && (attempt.Status == "success" || attempt.Status == "passed") && strings.TrimSpace(attempt.ResponseText) == "" {
			// No response is missing evidence, rather than a non-date answer.
			a.Verdict = "unknown"
			result.Status, result.ErrorMessage = "failed", "expected non-empty text output"
			suite.Answers = append(suite.Answers, a)
			break
		}
		if attempt.Status != "success" && attempt.Status != "passed" {
			// Transport/output-budget failures are execution failures, not proof of
			// diminished capability. Existing availability protection owns them.
			a.Verdict = "unknown"
			result.Status, result.ErrorMessage = attempt.Status, attempt.ErrorMessage
			suite.Answers = append(suite.Answers, a)
			break
		}
		if qualityCapabilityAnswerMatches(q, attempt.ResponseText) {
			a.Verdict = "pass"
			suite.Passed++
		}
		suite.Answers = append(suite.Answers, a)
		if suite.Passed >= c.RequiredPasses {
			suite.Verdict = "pass"
			break
		}
		if suite.Passed+c.Samples-len(suite.Answers) < c.RequiredPasses {
			suite.Verdict = "fail"
			break
		}
	}
	result.ExecutionInfo.Capability = suite
	result.ResponseText = fmt.Sprintf("%d/%d; %s", suite.Passed, len(suite.Answers), suite.Verdict)
	result.FinishedAt = time.Now()
	result.LatencyMs = result.FinishedAt.Sub(started).Milliseconds()
	return result
}
