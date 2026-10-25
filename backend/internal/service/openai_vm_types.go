package service

import (
	"context"
	"time"
)

// OpenAIVMConfig deliberately never serializes the encrypted bridge secret.
type OpenAIVMConfig struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	ProxyURL         string `json:"proxy_url"`
	SecretConfigured bool   `json:"secret_configured"`
	EncryptedSecret  string `json:"-"`
}

type OpenAIVMConfigInput struct {
	Enabled  bool   `json:"enabled"`
	BaseURL  string `json:"base_url"`
	ProxyURL string `json:"proxy_url"`
	Secret   string `json:"secret"`
}

type OpenAIVMBinding struct {
	AccountID          int64     `json:"account_id"`
	SlotID             string    `json:"slot_id"`
	Status             string    `json:"status"`
	RuntimeType        string    `json:"runtime_type"`
	LastError          string    `json:"last_error"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	ManagedProxyID     int64     `json:"-"`
	OriginalProxyID    *int64    `json:"-"`
	OriginalProxyIDs   []int64   `json:"-"`
	OriginalFallbackID *int64    `json:"-"`
}

type OpenAIVMRepository interface {
	GetConfig(context.Context) (*OpenAIVMConfig, error)
	SaveConfig(context.Context, *OpenAIVMConfig) error
	GetBinding(context.Context, int64) (*OpenAIVMBinding, error)
	// WithLock serializes account operations across server instances; ID 0
	// exclusively locks global configuration against concurrent provisioning.
	WithLock(context.Context, int64, func() error) error
	CountBindings(context.Context) (int, error)
	Reserve(context.Context, int64, string, *Proxy) (*OpenAIVMBinding, error)
	SetState(context.Context, int64, string, string, string) error
	Restore(context.Context, int64) error
}

func openAIVMOwnerID(a *Account) int64 {
	if a == nil {
		return 0
	}
	if a.ParentAccountID != nil {
		return *a.ParentAccountID
	}
	return a.ID
}

func (a *Account) IsOpenAIVMBound() bool {
	if a == nil {
		return false
	}
	bound, _ := a.Extra["openai_vm_bound"].(bool)
	return bound
}
