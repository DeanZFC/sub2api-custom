package vm2api

import "time"

type RuntimeType string

const (
	RuntimeDocker RuntimeType = "docker"
	RuntimeKVM    RuntimeType = "kvm"
)

type CreateSlotRequest struct {
	ID          string
	Name        string
	RuntimeType RuntimeType
	ProxyID     string
	Start       bool
}

type Slot struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Platform    string      `json:"platform"`
	Family      string      `json:"family"`
	RuntimeType RuntimeType `json:"runtime_type"`
	Status      string      `json:"status"`
	Schedulable bool        `json:"schedulable"`
	HasToken    bool        `json:"has_token"`
	Proxy       *Proxy      `json:"proxy,omitempty"`
	Runtime     struct {
		Type RuntimeType `json:"type"`
	} `json:"runtime"`
}

type SlotResult struct {
	VM Slot `json:"vm"`
	// Failure details are reduced to booleans to avoid exposing upstream secrets.
	StartFailed bool `json:"start_failed"`
	ProxyFailed bool `json:"proxy_failed"`
}

// AccessTokenCredential has deliberately no refresh_token or arbitrary map.
// ExpiresAt is encoded in Unix seconds, as expected by the Codex importer.
type AccessTokenCredential struct {
	AccessToken      string    `json:"-"`
	ChatGPTAccountID string    `json:"chatgpt_account_id"`
	Email            string    `json:"email,omitempty"`
	ExpiresAt        time.Time `json:"expires_at,omitempty"`
}

func (AccessTokenCredential) String() string     { return "vm2api.AccessTokenCredential{redacted}" }
func (a AccessTokenCredential) GoString() string { return a.String() }

type ManagedKey struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Key        string   `json:"-"`
	Category   string   `json:"category"`
	GroupType  string   `json:"group_type"`
	AllowedVMs []string `json:"allowed_vms"`
	Status     string   `json:"status"`
	VMPoolID   string   `json:"vm_pool_id,omitempty"`
}

func (ManagedKey) String() string     { return "vm2api.ManagedKey{redacted}" }
func (k ManagedKey) GoString() string { return k.String() }

type Proxy struct {
	ID         string   `json:"id"`
	Host       string   `json:"host"`
	Port       int      `json:"port"`
	Label      string   `json:"label,omitempty"`
	Enabled    bool     `json:"enabled"`
	BoundVMIDs []string `json:"bound_vm_ids,omitempty"`
}

type ProxyImportRequest struct {
	URL        string `json:"-"`
	BindSlotID string
}

func (ProxyImportRequest) String() string     { return "vm2api.ProxyImportRequest{redacted}" }
func (p ProxyImportRequest) GoString() string { return p.String() }

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}
