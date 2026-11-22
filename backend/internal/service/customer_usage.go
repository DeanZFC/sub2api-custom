package service

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// CustomerUsageRow contains the fields shared by personal sync and CSV. Amounts
// retain the database decimal representation so clients can reconcile exactly.
type CustomerUsageRow struct {
	ID                  int64     `json:"id"`
	CreatedAt           time.Time `json:"created_at"`
	RequestID           string    `json:"request_id"`
	APIKeyID            int64     `json:"api_key_id"`
	KeyName             string    `json:"api_key_name"`
	Model               string    `json:"model"`
	InputTokens         int       `json:"input_tokens"`
	OutputTokens        int       `json:"output_tokens"`
	CacheReadTokens     int       `json:"cache_read_tokens"`
	CacheCreationTokens int       `json:"cache_creation_tokens"`
	ActualCost          string    `json:"actual_cost"`
	TotalCost           string    `json:"total_cost"`
	RateMultiplier      string    `json:"rate_multiplier"`
	HTTPStatus          *int      `json:"http_status"`
	ReasoningEffort     string    `json:"reasoning_effort"`
	Endpoint            string    `json:"inbound_endpoint"`
	RequestType         string    `json:"request_type"`
	BillingMode         string    `json:"billing_mode"`
	DurationMS          *int      `json:"duration_ms"`
	FirstTokenMS        *int      `json:"first_token_ms"`
}
type CustomerUsageKey struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	DeletedAt *time.Time `json:"deleted_at"`
}
type UsageSyncPosition struct {
	XID   int64 `json:"x"`
	ID    int64 `json:"i"`
	Fence int64 `json:"f,omitempty"`
}

var ErrUsageExportBusy = errors.New("a usage export is already running")

type CustomerUsageRepository interface {
	Keys(context.Context, int64, pagination.PaginationParams) ([]CustomerUsageKey, int64, error)
	Key(context.Context, int64, int64) (*CustomerUsageKey, error)
	Sync(context.Context, usagestats.UsageLogFilters, UsageSyncPosition, int) ([]CustomerUsageRow, UsageSyncPosition, bool, error)
	Export(context.Context, usagestats.UsageLogFilters, pagination.PaginationParams, func(CustomerUsageRow) error) error
}
