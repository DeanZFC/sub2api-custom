// Package vmbridge implements the separately deployed, account-scoped vm2api bridge.
package vmbridge

import (
	"context"
	"net/http"
)

// ProvisionRequest contains an access token only. Refresh tokens are never accepted.
type ProvisionRequest struct {
	AccountID        int64  `json:"-"`
	Name             string `json:"name"`
	AccessToken      string `json:"access_token"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	UpstreamProxyURL string `json:"upstream_proxy_url"`
	RuntimeType      string `json:"runtime_type"`
}

type Identity struct {
	AccessToken      string `json:"access_token"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
}

// Resources identifies only resources belonging to this bridge account. APIKey
// is encrypted at rest and must never be returned by the management API.
type Resources struct {
	VMID    string `json:"vm_id"`
	KeyID   string `json:"key_id"`
	APIKey  string `json:"api_key"`
	ProxyID string `json:"proxy_id,omitempty"`
}

// Provider operations must be idempotent and account-scoped. Provision may
// return partial resources with an error so cleanup can still recover them.
// Forward accepts a request whose body is the untouched Responses body.
type Provider interface {
	Provision(context.Context, ProvisionRequest) (Resources, error)
	UpdateIdentity(context.Context, Resources, Identity) error
	Delete(context.Context, Resources) error
	Forward(context.Context, Resources, *http.Request) (*http.Response, error)
}

// ReadinessChecker is optional. Implementations backed by an external control
// plane can verify that the persisted slot and scoped key still exist before a
// management status is shown as ready. A failed check never mutates the
// lifecycle record; the next status request may recover automatically.
type ReadinessChecker interface {
	CheckReady(context.Context, Resources) error
}
