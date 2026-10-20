package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GPTDirectService is a narrow adapter for CTAI's public GPT redemption API.
// It never forwards browser cookies/auth, follows redirects, or retries writes.
type GPTDirectService struct {
	settings *SettingService
	client   *http.Client
	rewards  *gptDirectRewardStore
	worker   *gptDirectRewardWorker
}

func NewGPTDirectService(settings *SettingService) *GPTDirectService {
	return &GPTDirectService{settings: settings, client: &http.Client{
		Timeout:       45 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type GPTDirectInput struct {
	Code    string `json:"code"`
	Session string `json:"session"`
	ID      string `json:"id"`
	Token   string `json:"token"`
}

// Response DTOs deliberately exclude provider diagnostics and credentials.
type GPTDirectCode struct {
	Valid      bool   `json:"valid"`
	Product    string `json:"product"`
	Plan       string `json:"plan"`
	PlanName   string `json:"planName"`
	PlanKind   string `json:"planKind"`
	Status     string `json:"status"`
	Email      string `json:"email"`
	TaskStatus string `json:"taskStatus,omitempty"`
}

type GPTDirectTask struct {
	ID              string                  `json:"id"`
	Status          string                  `json:"status"`
	Progress        float64                 `json:"progress"`
	Email           string                  `json:"email"`
	Plan            string                  `json:"plan"`
	PlanName        string                  `json:"planName"`
	PlanKind        string                  `json:"planKind"`
	FulfillmentKind string                  `json:"fulfillmentKind"`
	Product         string                  `json:"product"`
	CreatedAt       string                  `json:"createdAt"`
	LotteryReward   *GPTDirectLotteryReward `json:"lottery_reward,omitempty"`
}

type GPTDirectReceipt struct {
	Task  GPTDirectTask `json:"task"`
	Token string        `json:"token"`
}

type GPTDirectSubscription struct {
	Email                 string `json:"email"`
	Plan                  string `json:"plan"`
	PlanKey               string `json:"planKey"`
	Active                bool   `json:"active"`
	HasActiveSubscription bool   `json:"hasActiveSubscription"`
	ExpiresAt             string `json:"expiresAt"`
	RemainingDays         *int   `json:"remainingDays"`
}

type GPTDirectError struct {
	Status     int
	Reason     string
	RetryAfter int
}

func (e *GPTDirectError) Error() string { return "GPT direct request failed: " + e.Reason }

func gptDirectError(status int, reason string) *GPTDirectError {
	return &GPTDirectError{Status: status, Reason: reason}
}

var gptDirectKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,160}$`)
var gptDirectTaskPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var gptDirectTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func (s *GPTDirectService) Call(ctx context.Context, operation string, input GPTDirectInput, userID int64, clientIP, idempotencyKey string) (any, *GPTDirectError) {
	config, err := s.settings.GetGPTDirectConfig(ctx)
	if err != nil {
		return nil, gptDirectError(503, "CONFIG_UNAVAILABLE")
	}
	if !config.Public().Enabled {
		return nil, gptDirectError(404, "DISABLED")
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	var attempt gptDirectAttempt
	if operation == "redeem" {
		if len(input.Code) < 4 || len(input.Code) > 80 {
			return nil, gptDirectError(400, "CODE_INVALID_INPUT")
		}
		if strings.TrimSpace(input.Session) == "" || len(input.Session) > 64*1024 {
			return nil, gptDirectError(400, "SESSION_REQUIRED")
		}
		if userID <= 0 || !gptDirectKeyPattern.MatchString(idempotencyKey) {
			return nil, gptDirectError(400, "IDEMPOTENCY_REQUIRED")
		}
		if config.LotteryRewardEnabled && !s.RewardsReady() {
			return nil, gptDirectError(503, "REWARD_UNAVAILABLE")
		}
		if s.rewards != nil {
			attempt, err = s.rewards.prepare(ctx, config, userID, input.Code, idempotencyKey)
			if err != nil {
				return nil, gptDirectError(503, "REWARD_TRACKING_UNAVAILABLE")
			}
			if attempt.Chances > 0 && !s.RewardsReady() {
				return nil, gptDirectError(503, "REWARD_UNAVAILABLE")
			}
			// Keep accepting the upstream receipt after the browser closes. The
			// bounded request never retries a recharge on its own.
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 50*time.Second)
			defer cancel()
		}
	}
	result, failure := s.call(ctx, config, operation, input, userID, clientIP, idempotencyKey)
	// A lost receipt can leave the CDK processing or used. Only the original
	// website account and persisted request key may proceed to an idempotent
	// replay; querying a code alone cannot claim or restore its reward.
	if operation == "verify" && failure != nil && s.rewards != nil &&
		(failure.Reason == "CODE_PROCESSING" || failure.Reason == "CODE_USED") &&
		s.rewards.canResume(ctx, config, userID, input.Code, idempotencyKey) {
		return s.call(ctx, config, "query", input, userID, clientIP, "")
	}
	if failure != nil || s.rewards == nil {
		return result, failure
	}
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	switch value := result.(type) {
	case GPTDirectReceipt:
		if err = s.rewards.record(settleCtx, attempt, &value); err != nil {
			return nil, gptDirectError(503, "REWARD_TRACKING_UNAVAILABLE")
		}
		value.Task.LotteryReward, err = s.rewards.observe(settleCtx, gptDirectScope(config), userID, value.Token, &value.Task)
		result = value
	case GPTDirectTask:
		value.LotteryReward, err = s.rewards.observe(settleCtx, gptDirectScope(config), userID, input.Token, &value)
		result = value
	}
	if err != nil {
		return nil, gptDirectError(503, "REWARD_TRACKING_UNAVAILABLE")
	}
	return result, nil
}

func (s *GPTDirectService) call(ctx context.Context, config GPTDirectConfig, operation string, input GPTDirectInput, userID int64, clientIP, idempotencyKey string) (any, *GPTDirectError) {
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Session = strings.TrimSpace(input.Session)
	body := map[string]string{"product": "gpt", "tenantSlug": config.TenantSlug}
	method, path := http.MethodPost, operation
	switch operation {
	case "verify", "query", "redeem":
		if len(input.Code) < 4 || len(input.Code) > 80 {
			return nil, gptDirectError(400, "CODE_INVALID_INPUT")
		}
		body["code"] = input.Code
	case "subscription":
	case "task":
		if !gptDirectTaskPattern.MatchString(input.ID) || !gptDirectTokenPattern.MatchString(input.Token) {
			return nil, gptDirectError(400, "TASK_TOKEN_REQUIRED")
		}
		method, path = http.MethodGet, "tasks/"+input.ID+"?token="+url.QueryEscape(input.Token)
	default:
		return nil, gptDirectError(404, "NOT_FOUND")
	}
	if operation == "redeem" || operation == "subscription" {
		if input.Session == "" || len(input.Session) > 64*1024 {
			return nil, gptDirectError(400, "SESSION_REQUIRED")
		}
		body["session"] = input.Session
	}
	if operation == "redeem" && (userID <= 0 || !gptDirectKeyPattern.MatchString(idempotencyKey)) {
		return nil, gptDirectError(400, "IDEMPOTENCY_REQUIRED")
	}
	encoded, _ := json.Marshal(body)
	var reader io.Reader
	if method == http.MethodPost {
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, config.BaseURL+"/api/v1/public/"+path, reader)
	if err != nil {
		return nil, gptDirectError(503, "CONFIG_UNAVAILABLE")
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	// Only our trusted client-IP resolver supplies this header. CTAI must trust
	// this server's egress address via TRUSTED_PROXY_CIDRS to keep per-IP quotas.
	if net.ParseIP(clientIP) != nil {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	if operation == "redeem" {
		key := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", userID, idempotencyKey)))
		req.Header.Set("Idempotency-Key", fmt.Sprintf("sub2api-gpt-%x", key))
	}
	res, err := s.client.Do(req)
	if err != nil {
		// Do not wrap transport errors: they may include the task-token URL.
		return nil, gptDirectError(502, "UPSTREAM_UNAVAILABLE")
	}
	defer func() { _ = res.Body.Close() }()
	const maxResponse = 256 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return nil, gptDirectError(502, "UPSTREAM_UNAVAILABLE")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		failure := gptDirectError(502, "UPSTREAM_UNAVAILABLE")
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		if res.StatusCode >= 400 && res.StatusCode < 500 && gptDirectBusinessErrors[envelope.Error.Code] {
			failure.Status, failure.Reason = res.StatusCode, envelope.Error.Code
		}
		if res.StatusCode == 429 {
			failure.Status, failure.Reason, failure.RetryAfter = 429, "RATE_LIMIT", 60
			if seconds, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && seconds > 0 {
				failure.RetryAfter = min(seconds, 3600)
			}
		}
		return nil, failure
	}
	invalid := gptDirectError(502, "INVALID_RESPONSE")
	switch operation {
	case "verify", "query":
		var result GPTDirectCode
		if json.Unmarshal(raw, &result) != nil || !result.Valid || result.Product != "gpt" || result.Plan == "" {
			return nil, invalid
		}
		return result, nil
	case "subscription":
		var result GPTDirectSubscription
		if json.Unmarshal(raw, &result) != nil || result.Email == "" {
			return nil, invalid
		}
		return result, nil
	case "redeem":
		var result GPTDirectReceipt
		if json.Unmarshal(raw, &result) != nil || !validGPTDirectTask(&result.Task) || !gptDirectTokenPattern.MatchString(result.Token) {
			return nil, invalid
		}
		result.Task.LotteryReward = nil
		return result, nil
	default:
		var result GPTDirectTask
		if json.Unmarshal(raw, &result) != nil || !validGPTDirectTask(&result) || result.ID != input.ID {
			return nil, invalid
		}
		result.LotteryReward = nil
		return result, nil
	}
}

func validGPTDirectTask(task *GPTDirectTask) bool {
	if !gptDirectTaskPattern.MatchString(task.ID) || task.Product != "gpt" {
		return false
	}
	switch task.Status {
	case "manual_pending", "queued", "running", "success", "failed", "cancelled":
		task.Progress = math.Max(0, math.Min(100, task.Progress))
		return true
	default:
		return false
	}
}

var gptDirectBusinessErrors = map[string]bool{
	"CODE_NOT_FOUND": true, "CODE_PRODUCT_MISMATCH": true, "CODE_TENANT_MISMATCH": true,
	"CODE_USED": true, "CODE_PROCESSING": true, "CODE_DISABLED": true, "CODE_UNAVAILABLE": true,
	"CODE_INVALID_INPUT": true, "INVALID_SESSION": true, "SESSION_REQUIRED": true,
	"SESSION_EXPIRED": true, "INVALID_ACCESS_TOKEN": true, "TASK_TOKEN_REQUIRED": true,
	"IDEMPOTENCY_CONFLICT": true, "PLAN_UNAVAILABLE": true, "VALIDATION_ERROR": true,
	"AGENT_LINK_NOT_FOUND": true, "AGENT_DISABLED": true, "AGENT_FROZEN": true,
}
