package service

import (
	"context"
	"time"
)

type OpenAIIPv6Binding struct {
	AccountID          int64     `json:"account_id"`
	LeaseID            string    `json:"lease_id"`
	Address            string    `json:"address"`
	Status             string    `json:"status"`
	LastError          string    `json:"last_error"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ManagedProxyID     int64     `json:"-"`
	OriginalProxyID    *int64    `json:"-"`
	OriginalProxyIDs   []int64   `json:"-"`
	OriginalFallbackID *int64    `json:"-"`
	ControlURL         string    `json:"-"`
}
type OpenAIIPv6Repository interface {
	GetBinding(context.Context, int64) (*OpenAIIPv6Binding, error)
	WithLock(context.Context, int64, func() error) error
	Reserve(context.Context, int64, string, string, *Proxy) (*OpenAIIPv6Binding, error)
	SetState(context.Context, int64, string, string) error
	Activate(context.Context, int64, string, string) error
	Restore(context.Context, int64) error
}

func (a *Account) IsOpenAIIPv6Bound() bool {
	if a == nil {
		return false
	}
	v, _ := a.Extra["openai_ipv6_bound"].(bool)
	return v
}
