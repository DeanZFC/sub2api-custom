package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
)

var ErrSharedListingNotFound = infraerrors.NotFound("SHARED_LISTING_NOT_FOUND", "shared account listing not found")

// SharedAccountCard is the intentionally redacted payload used by the pool UI.
// Credentials, proxy secrets and consumer identities must never be added here.
type SharedAccountCard struct {
	ID                    int64                     `json:"id"`
	AccountID             int64                     `json:"account_id,omitempty"`
	Platform              string                    `json:"platform"`
	DisplayName           string                    `json:"display_name"`
	Status                string                    `json:"status"`
	ConcurrencyLimit      int                       `json:"concurrency_limit"`
	ConcurrencyMultiplier float64                   `json:"concurrency_multiplier"`
	SellRate              float64                   `json:"sell_rate"`
	TotalCallCount        int64                     `json:"total_call_count"`
	LastCalledAt          *time.Time                `json:"last_called_at,omitempty"`
	RecentCalls           []SharedAccountRecentCall `json:"recent_calls"`
}

type SharedAccountRecentCall struct {
	RequestID     string    `json:"request_id"`
	Model         string    `json:"model,omitempty"`
	ResultStatus  string    `json:"result_status"`
	DurationMS    int64     `json:"duration_ms,omitempty"`
	ChargedAmount float64   `json:"charged_amount"`
	CreatedAt     time.Time `json:"created_at"`
}

type SharedAccountPoolRepository interface {
	ListPublicCards(ctx context.Context, platform string, limit, recentLimit int) ([]SharedAccountCard, error)
	GetOwnerCards(ctx context.Context, ownerID int64, limit, recentLimit int) ([]SharedAccountCard, error)
	CreateListing(ctx context.Context, listing *SharedAccountListing) error
	SetListingStatus(ctx context.Context, ownerID, listingID int64, status string) error
	DeleteListing(ctx context.Context, ownerID, listingID int64) error
}

type SharedAccountListing struct {
	ID, OwnerUserID, AccountID      int64
	Platform, DisplayName, Status   string
	ConcurrencyLimit                int
	ConcurrencyMultiplier, SellRate float64
}

type SharedAccountUploadInput struct {
	Name, Platform, Type            string
	Credentials                     map[string]any
	Extra                           map[string]any
	ExpiresAt                       *time.Time
	Concurrency                     int
	ConcurrencyMultiplier, SellRate float64
	ProxyURL                        string
}

type SharedAccountHealthChecker interface {
	Check(ctx context.Context, accountID int64, platform string) error
}

type accountTestHealthChecker struct{ tester *AccountTestService }

func (h *accountTestHealthChecker) Check(ctx context.Context, accountID int64, platform string) error {
	if h == nil || h.tester == nil {
		return nil
	}
	gin.SetMode(gin.ReleaseMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/internal/shared-account-health", nil).WithContext(ctx)
	return h.tester.TestAccountConnection(c, accountID, "", "health check", AccountTestModeDefault)
}

type SharedAccountUploadService struct {
	accounts AccountRepository
	listings SharedAccountPoolRepository
	groups   GroupRepository
	proxies  ProxyRepository
	health   SharedAccountHealthChecker
}

func ProvideSharedAccountUploadService(accounts AccountRepository, listings SharedAccountPoolRepository, groups GroupRepository, proxies ProxyRepository, tester *AccountTestService) *SharedAccountUploadService {
	s := NewSharedAccountUploadService(accounts, listings, groups, proxies)
	if tester != nil {
		s.SetHealthChecker(&accountTestHealthChecker{tester: tester})
	}
	return s
}

func NewSharedAccountUploadService(accounts AccountRepository, listings SharedAccountPoolRepository, groups GroupRepository, proxies ProxyRepository) *SharedAccountUploadService {
	return &SharedAccountUploadService{accounts: accounts, listings: listings, groups: groups, proxies: proxies}
}
func (s *SharedAccountUploadService) SetHealthChecker(checker SharedAccountHealthChecker) {
	s.health = checker
}

func (s *SharedAccountUploadService) Upload(ctx context.Context, ownerID int64, in SharedAccountUploadInput) (*SharedAccountListing, error) {
	var proxyID *int64
	cleanupProxy := func() {
		if proxyID != nil && s.proxies != nil {
			_ = s.proxies.Delete(ctx, *proxyID)
		}
	}
	if ownerID <= 0 {
		return nil, ErrUserNotFound
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Platform = strings.ToLower(strings.TrimSpace(in.Platform))
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	if in.Name == "" || len([]rune(in.Name)) > 100 || in.Platform == "" || in.Type == "" || len(in.Credentials) == 0 {
		return nil, errors.New("name, platform, type and credentials are required")
	}
	if !IsAllowedQuotaPlatform(in.Platform) {
		return nil, infraerrors.BadRequest("INVALID_SHARED_PLATFORM", "unsupported shared account platform")
	}
	switch in.Type {
	case AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken, AccountTypeBedrock, AccountTypeServiceAccount:
	default:
		return nil, infraerrors.BadRequest("INVALID_SHARED_ACCOUNT_TYPE", "unsupported shared account type")
	}
	if err := validateSharedCredentials(in); err != nil {
		return nil, err
	}

	if in.Concurrency <= 0 {
		in.Concurrency = 1
	}
	if in.Concurrency > 1000 {
		return nil, errors.New("concurrency must be <= 1000")
	}
	if in.ConcurrencyMultiplier <= 0 {
		in.ConcurrencyMultiplier = 1
	}
	if in.ConcurrencyMultiplier > 5 || in.SellRate < 0 || in.SellRate > 100 {
		return nil, errors.New("invalid sharing rates")
	}
	if math.IsNaN(in.ConcurrencyMultiplier) || math.IsInf(in.ConcurrencyMultiplier, 0) || math.IsNaN(in.SellRate) || math.IsInf(in.SellRate, 0) {
		return nil, errors.New("invalid sharing rates")
	}
	proxy, err := s.CreateProxy(ctx, ownerID, in.ProxyURL)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		proxyID = &proxy.ID
	}

	// Keep the account out of scheduling until its owner listing is durably created.
	// Publication is automatic; this staging step is not an approval queue.
	account := &Account{Name: in.Name, Platform: in.Platform, Type: in.Type, Credentials: SanitizeStoredCredentials(in.Platform, in.Credentials), Extra: sharedAccountExtra(in.Extra), ExpiresAt: in.ExpiresAt, AutoPauseOnExpired: true, Concurrency: max(1, int(math.Floor(float64(in.Concurrency)*in.ConcurrencyMultiplier))), RateMultiplier: &in.SellRate, Priority: 50, Status: StatusActive, Schedulable: false, AccountScope: "shared", ProxyID: proxyID}
	if err := s.accounts.Create(ctx, account); err != nil {
		cleanupProxy()
		return nil, fmt.Errorf("create shared account: %w", err)
	}
	if s.groups != nil {
		groupName := "shared-" + in.Platform
		groups, err := s.groups.ListActiveByPlatform(ctx, in.Platform)
		if err != nil {
			cleanupProxy()
			_ = s.accounts.Delete(ctx, account.ID)
			return nil, fmt.Errorf("find shared group: %w", err)
		}
		var sharedGroup *Group
		for i := range groups {
			if groups[i].IsSharedPool {
				sharedGroup = &groups[i]
				break
			}
		}
		if sharedGroup == nil {
			sharedGroup = &Group{IsSharedPool: true, Name: groupName, Platform: in.Platform, Status: StatusActive, RateMultiplier: 1, IsExclusive: false, SubscriptionType: "standard"}
			if err := s.groups.Create(ctx, sharedGroup); err != nil {
				cleanupProxy()
				_ = s.accounts.Delete(ctx, account.ID)
				return nil, fmt.Errorf("create shared group: %w", err)
			}
		}
		if err := s.accounts.BindGroups(ctx, account.ID, []int64{sharedGroup.ID}); err != nil {
			cleanupProxy()
			_ = s.accounts.Delete(ctx, account.ID)
			return nil, fmt.Errorf("bind shared group: %w", err)
		}
	}
	listing := &SharedAccountListing{OwnerUserID: ownerID, AccountID: account.ID, Platform: in.Platform, DisplayName: in.Name, Status: "active", ConcurrencyLimit: in.Concurrency, ConcurrencyMultiplier: in.ConcurrencyMultiplier, SellRate: in.SellRate}
	if err := s.listings.CreateListing(ctx, listing); err != nil {
		cleanupProxy()
		_ = s.accounts.Delete(ctx, account.ID)
		return nil, fmt.Errorf("create shared listing: %w", err)
	}
	// Keep newly uploaded shared accounts immediately usable, matching the
	// account-management create flow.  A background probe cannot reliably
	// determine whether credentials are usable here (it has no request model
	// or caller context) and previously marked otherwise valid accounts as
	// `error`/unschedulable right after publication.  Owners can use the
	// explicit "测试连接" action to validate an account and inspect errors.
	return listing, nil
}

// CreateProxy validates the URL for both authorization and final account creation.
// Authorization callers must delete this temporary proxy after the operation.
func (s *SharedAccountUploadService) CreateProxy(ctx context.Context, ownerID int64, rawURL string) (*Proxy, error) {
	if strings.TrimSpace(rawURL) != "" {
		_, parsed, err := proxyurl.Parse(rawURL)
		if err != nil {
			return nil, err
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" || strings.HasSuffix(host, ".local") || host == "metadata.google.internal" {
			return nil, errors.New("proxy host is not allowed")
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
			return nil, errors.New("proxy host is not allowed")
		}
		if s.proxies == nil {
			return nil, errors.New("proxy support is unavailable")
		}
		port, err := strconv.Atoi(parsed.Port())
		if err != nil || port <= 0 || port > 65535 {
			return nil, errors.New("proxy URL must include a valid port")
		}
		p := &Proxy{OwnerUserID: &ownerID, Name: "shared authorization proxy", Protocol: parsed.Scheme, Host: parsed.Hostname(), Port: port, Status: StatusActive, FallbackMode: FallbackModeNone}
		if parsed.User != nil {
			p.Username = parsed.User.Username()
			p.Password, _ = parsed.User.Password()
		}
		if err := s.proxies.Create(ctx, p); err != nil {
			return nil, fmt.Errorf("create shared proxy: %w", err)
		}
		return p, nil
	}
	return nil, nil
}

func (s *SharedAccountUploadService) DeleteAuthProxy(ctx context.Context, proxy *Proxy) {
	if proxy != nil && s.proxies != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = s.proxies.Delete(cleanupCtx, proxy.ID)
	}
}

func sharedAccountExtra(extra map[string]any) map[string]any {
	result := map[string]any{}
	for _, key := range []string{"account_mode", "api_protocol", "email", "name", "privacy_mode", "subscription_tier", "project_id"} {
		if value, ok := extra[key]; ok {
			result[key] = value
		}
	}
	return result
}

func validateSharedCredentials(in SharedAccountUploadInput) error {
	account := &Account{Platform: in.Platform, Type: in.Type, Credentials: in.Credentials}
	keys := []string{"access_token"}
	switch in.Type {
	case AccountTypeAPIKey:
		keys = []string{"api_key"}
	case AccountTypeBedrock:
		if in.Platform != PlatformAnthropic {
			return infraerrors.BadRequest("INVALID_SHARED_ACCOUNT_TYPE", "Bedrock requires Anthropic")
		}
		keys = []string{"aws_access_key_id", "aws_secret_access_key", "aws_region"}
		if account.GetCredential("auth_mode") == "apikey" {
			keys = []string{"api_key", "aws_region"}
		}
	case AccountTypeServiceAccount:
		if in.Platform != PlatformAnthropic && in.Platform != PlatformGemini {
			return infraerrors.BadRequest("INVALID_SHARED_ACCOUNT_TYPE", "Vertex requires Anthropic or Gemini")
		}
		keys = []string{"service_account_json"}
	case AccountTypeSetupToken:
		if in.Platform != PlatformAnthropic {
			return infraerrors.BadRequest("INVALID_SHARED_ACCOUNT_TYPE", "Setup Token requires Anthropic")
		}
	case AccountTypeOAuth:
		if in.Platform == PlatformOpenAI && account.IsOpenAIAgentIdentity() {
			keys = []string{"agent_runtime_id", "agent_private_key"}
		}
	}
	for _, key := range keys {
		if strings.TrimSpace(account.GetCredential(key)) == "" {
			return infraerrors.BadRequest("INVALID_SHARED_CREDENTIALS", key+" is required")
		}
	}
	return nil
}
