package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const statusClientClosedRequest = 499

const (
	gatewayQueueFullCode          = "gateway_queue_full"
	gatewayConcurrencyLimitCode   = "gateway_concurrency_limit"
	gatewayAccountUnavailableCode = "gateway_account_unavailable"
)

func concurrencyErrorResponse(err error, slotType string) (int, string, string, string) {
	if errors.Is(err, service.ErrAccountAdmissionDenied) {
		return http.StatusServiceUnavailable, "api_error", gatewayAccountUnavailableCode,
			"Account temporarily unavailable due to scheduling or quality protection, please retry later"
	}

	var waitQueueFullErr *WaitQueueFullError
	if errors.As(err, &waitQueueFullErr) {
		return http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode,
			"Too many pending requests, please retry later"
	}

	var concurrencyErr *ConcurrencyError
	if errors.As(err, &concurrencyErr) {
		if concurrencyErr.SlotType != "" {
			slotType = concurrencyErr.SlotType
		}
		return http.StatusTooManyRequests, "rate_limit_error", gatewayConcurrencyLimitCode,
			fmt.Sprintf("Concurrency limit exceeded for %s, please retry later", slotType)
	}

	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, "api_error", "", "context canceled"
	}

	return http.StatusServiceUnavailable, "api_error", "", "Service temporarily unavailable, please retry later"
}
