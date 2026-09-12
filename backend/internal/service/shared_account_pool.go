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
	Type                  string                    `json:"type,omitempty"`
	DisplayName           string                    `json:"display_name"`
	UploaderName          string                    `json:"uploader_name,omitempty"`
	Status                string                    `json:"status"`
	ConcurrencyLimit      int                       `json:"concurrency_limit"`
	ConcurrencyMultiplier float64                   `json:"concurrency_multiplier"`
	SellRate              float64                   `json:"sell_rate"`
	Listed                bool                      `json:"listed"`
	CurrentConcurrency    int                       `json:"current_concurrency"`
	TotalCallCount        int64                     `json:"total_call_count"`
	LastCalledAt          *time.Time                `json:"last_called_at,omitempty"`
	AvailableModels       []string                  `json:"available_models,omitempty"`
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
	GetOwnerListingByAccountID(ctx context.Context, ownerID, accountID int64) (*SharedAccountListing, error)
	CreateListing(ctx context.Context, listing *SharedAccountListing) error
	UpdateListingMeta(ctx context.Context, ownerID, listingID int64, displayName string, concurrency int, sellRate float64) error
	SetListingStatus(ctx context.Context, ownerID, listingID int64, status string) error
	SetListingListed(ctx context.Context, ownerID, listingID int64, listed bool) error
	DeleteListing(ctx context.Context, ownerID, listingID int64) error
}

// SharedAccountPoolAdminRepository is the optional moderation contract. It is
// separate from the user-facing repository interface so lightweight service
// fakes do not need to implement admin-only methods.
type SharedAccountPoolAdminRepository interface {
	ListAdminCards(ctx context.Context, platform, status, search string, ownerID *int64, limit, recentLimit int) ([]SharedAccountCard, error)
	ListAdminUsers(ctx context.Context, search string, limit int) ([]SharedPoolUserSummary, error)
	SetListingAdminStatus(ctx context.Context, listingID int64, status string) error
	SetListingAdminListed(ctx context.Context, listingID int64, listed bool) error
	SetUserSharedPublishPermission(ctx context.Context, userID int64, enabled bool, reason string, until *time.Time) error
	IsUserSharedPublishAllowed(ctx context.Context, userID int64) (bool, error)
}

type SharedPoolUserSummary struct {
	UserID             int64      `json:"user_id"`
	Username           string     `json:"username,omitempty"`
	Email              string     `json:"email,omitempty"`
	SharedAccountCount int        `json:"shared_account_count"`
	PublishEnabled     bool       `json:"publish_enabled"`
	BlockedUntil       *time.Time `json:"blocked_until,omitempty"`
	BlockReason        string     `json:"block_reason,omitempty"`
}

type SharedAccountListing struct {
	ID, OwnerUserID, AccountID      int64
	Platform, DisplayName, Status   string
	ConcurrencyLimit                int
	ConcurrencyMultiplier, SellRate float64
	TotalCallCount                  int64
	Listed                          bool
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
	tester   *AccountTestService
}

func ProvideSharedAccountUploadService(accounts AccountRepository, listings SharedAccountPoolRepository, groups GroupRepository, proxies ProxyRepository, tester *AccountTestService) *SharedAccountUploadService {
	s := NewSharedAccountUploadService(accounts, listings, groups, proxies)
	s.tester = tester
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
	if adminRepo, ok := s.listings.(SharedAccountPoolAdminRepository); ok {
		allowed, err := adminRepo.IsUserSharedPublishAllowed(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, infraerrors.Forbidden("SHARED_PUBLISH_DISABLED", "shared account publishing is disabled for this user")
		}
	}
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
	listing := &SharedAccountListing{OwnerUserID: ownerID, AccountID: account.ID, Platform: in.Platform, DisplayName: in.Name, Status: "active", ConcurrencyLimit: in.Concurrency, ConcurrencyMultiplier: in.ConcurrencyMultiplier, SellRate: in.SellRate, Listed: true}
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

type SharedAccountUpdateInput struct {
	Name         *string
	Credentials  *map[string]any
	Extra        *map[string]any
	Concurrency  *int
	SellRate     *float64
	ExpiresAt    *time.Time
	ClearExpiry  bool
	ProxyURL     string
	Type         string
	ReplaceCreds bool
}

func (s *SharedAccountUploadService) ownedAccount(ctx context.Context, ownerID, accountID int64) (*Account, *SharedAccountListing, error) {
	if s == nil || s.accounts == nil || s.listings == nil || ownerID <= 0 || accountID <= 0 {
		return nil, nil, ErrSharedListingNotFound
	}
	listing, err := s.listings.GetOwnerListingByAccountID(ctx, ownerID, accountID)
	if err != nil {
		return nil, nil, err
	}
	account, err := s.accounts.GetByID(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	if account == nil || account.AccountScope != "shared" {
		return nil, nil, ErrSharedListingNotFound
	}
	account.Concurrency = listing.ConcurrencyLimit
	account.RateMultiplier = &listing.SellRate
	account.Name = listing.DisplayName
	return account, listing, nil
}

func (s *SharedAccountUploadService) GetOwned(ctx context.Context, ownerID, accountID int64) (*Account, error) {
	account, _, err := s.ownedAccount(ctx, ownerID, accountID)
	return account, err
}

func (s *SharedAccountUploadService) GetOwnedDetail(ctx context.Context, ownerID, accountID int64) (*Account, *SharedAccountListing, error) {
	return s.ownedAccount(ctx, ownerID, accountID)
}

func (s *SharedAccountUploadService) TestOwnedAccount(c *gin.Context, ownerID, accountID int64, modelID, prompt, mode string, opts AccountTestOptions) error {
	if s == nil || s.tester == nil {
		return infraerrors.InternalServer("ACCOUNT_TEST_UNAVAILABLE", "account test service is not configured")
	}
	if _, err := s.GetOwned(c.Request.Context(), ownerID, accountID); err != nil {
		return err
	}
	return s.tester.TestAccountConnection(c, accountID, modelID, prompt, mode, opts)
}

func (s *SharedAccountUploadService) SyncOwnedUpstreamModels(ctx context.Context, ownerID, accountID int64) (*UpstreamModelCatalog, error) {
	if s == nil || s.tester == nil {
		return nil, infraerrors.InternalServer("ACCOUNT_TEST_UNAVAILABLE", "account test service is not configured")
	}
	account, err := s.GetOwned(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	return s.tester.SyncUpstreamModelCatalog(ctx, account)
}

func (s *SharedAccountUploadService) SyncUpstreamModelsPreview(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	if s == nil || s.tester == nil {
		return nil, infraerrors.InternalServer("ACCOUNT_TEST_UNAVAILABLE", "account test service is not configured")
	}
	if account == nil {
		return nil, infraerrors.BadRequest("INVALID_REQUEST", "account is required")
	}
	return s.tester.SyncUpstreamModelCatalog(ctx, account)
}

func (s *SharedAccountUploadService) UpdateOwned(ctx context.Context, ownerID, accountID int64, in SharedAccountUpdateInput) (*Account, error) {
	account, listing, err := s.ownedAccount(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len([]rune(name)) > 100 {
			return nil, errors.New("name is required")
		}
		account.Name = name
		listing.DisplayName = name
	}
	if in.Concurrency != nil {
		if *in.Concurrency <= 0 {
			return nil, errors.New("concurrency must be >= 1")
		}
		if *in.Concurrency > 1000 {
			return nil, errors.New("concurrency must be <= 1000")
		}
		listing.ConcurrencyLimit = *in.Concurrency
		mult := listing.ConcurrencyMultiplier
		if mult <= 0 {
			mult = 1
		}
		account.Concurrency = max(1, int(math.Floor(float64(*in.Concurrency)*mult)))
	}
	if in.SellRate != nil {
		if *in.SellRate < 0 || *in.SellRate > 100 || math.IsNaN(*in.SellRate) || math.IsInf(*in.SellRate, 0) {
			return nil, errors.New("invalid sharing rates")
		}
		if listing.Status == "active" && listing.TotalCallCount > 0 && *in.SellRate > listing.SellRate+1e-6 {
			return nil, infraerrors.BadRequest("SHARED_SELL_RATE_LOCKED", "使用中不能提高倍率。请先暂停账号，改完后再恢复上线。")
		}
		listing.SellRate = *in.SellRate
		account.RateMultiplier = in.SellRate
	}
	if in.Credentials != nil {
		merged := MergePreservingSensitiveCreds(account.Credentials, *in.Credentials)
		account.Credentials = SanitizeStoredCredentials(account.Platform, merged)
		if err := validateSharedCredentials(SharedAccountUploadInput{Platform: account.Platform, Type: account.Type, Credentials: account.Credentials}); err != nil {
			return nil, err
		}
	}
	if in.Extra != nil {
		extra := map[string]any{}
		for key, value := range account.Extra {
			extra[key] = value
		}
		for key, value := range sharedAccountExtra(*in.Extra) {
			extra[key] = value
		}
		account.Extra = extra
	}
	if in.ClearExpiry {
		account.ExpiresAt = nil
	} else if in.ExpiresAt != nil {
		account.ExpiresAt = in.ExpiresAt
	}
	if strings.TrimSpace(in.ProxyURL) != "" {
		proxy, err := s.CreateProxy(ctx, ownerID, in.ProxyURL)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			account.ProxyID = &proxy.ID
		}
	}
	if err := s.accounts.Update(ctx, account); err != nil {
		return nil, fmt.Errorf("update shared account: %w", err)
	}
	if err := s.listings.UpdateListingMeta(ctx, ownerID, listing.ID, listing.DisplayName, listing.ConcurrencyLimit, listing.SellRate); err != nil {
		return nil, fmt.Errorf("update shared listing: %w", err)
	}
	return s.GetOwned(ctx, ownerID, accountID)
}

func (s *SharedAccountUploadService) ApplyOwnedOAuthCredentials(ctx context.Context, ownerID, accountID int64, accountType string, credentials, extra map[string]any) (*Account, error) {
	account, listing, err := s.ownedAccount(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsOAuth() {
		return nil, infraerrors.BadRequest("NOT_OAUTH", "cannot apply oauth credentials to non-OAuth account")
	}
	if accountType == AccountTypeSetupToken {
		account.Type = AccountTypeSetupToken
	} else if accountType != "" {
		account.Type = AccountTypeOAuth
	}
	account.Credentials = SanitizeStoredCredentials(account.Platform, credentials)
	if err := validateSharedCredentials(SharedAccountUploadInput{Platform: account.Platform, Type: account.Type, Credentials: account.Credentials}); err != nil {
		return nil, err
	}
	if extra != nil {
		merged := map[string]any{}
		for key, value := range account.Extra {
			merged[key] = value
		}
		for key, value := range sharedAccountExtra(extra) {
			merged[key] = value
		}
		account.Extra = merged
	}
	account.Status = StatusActive
	account.ErrorMessage = ""
	if listing.Status == "active" {
		account.Schedulable = true
	}
	if err := s.accounts.Update(ctx, account); err != nil {
		return nil, fmt.Errorf("update shared account: %w", err)
	}
	if err := s.accounts.ClearError(ctx, account.ID); err != nil {
		return nil, err
	}
	return s.GetOwned(ctx, ownerID, accountID)
}

func (s *SharedAccountUploadService) ClearOwnedError(ctx context.Context, ownerID, accountID int64) (*Account, error) {
	account, listing, err := s.ownedAccount(ctx, ownerID, accountID)
	if err != nil {
		return nil, err
	}
	if err := s.accounts.ClearError(ctx, account.ID); err != nil {
		return nil, err
	}
	if listing.Status == "active" {
		if err := s.accounts.SetSchedulable(ctx, account.ID, true); err != nil {
			return nil, err
		}
	}
	return s.GetOwned(ctx, ownerID, accountID)
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
