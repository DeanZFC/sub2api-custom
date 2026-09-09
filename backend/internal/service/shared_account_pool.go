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
	case AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken:
	default:
		return nil, infraerrors.BadRequest("INVALID_SHARED_ACCOUNT_TYPE", "unsupported shared account type")
	}
	credentialKey := "api_key"
	if in.Type != AccountTypeAPIKey {
		credentialKey = "access_token"
	}
	credential, _ := in.Credentials[credentialKey].(string)
	if strings.TrimSpace(credential) == "" {
		return nil, infraerrors.BadRequest("INVALID_SHARED_CREDENTIALS", credentialKey+" is required")
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
	if strings.TrimSpace(in.ProxyURL) != "" {
		_, parsed, err := proxyurl.Parse(in.ProxyURL)
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
		p := &Proxy{OwnerUserID: &ownerID, Name: in.Name + " proxy", Protocol: parsed.Scheme, Host: parsed.Hostname(), Port: port, Status: StatusActive, FallbackMode: FallbackModeNone}
		if parsed.User != nil {
			p.Username = parsed.User.Username()
			p.Password, _ = parsed.User.Password()
		}
		if err := s.proxies.Create(ctx, p); err != nil {
			return nil, fmt.Errorf("create shared proxy: %w", err)
		}
		proxyID = &p.ID
	}
	// Keep the account out of scheduling until its owner listing is durably created.
	// Publication is automatic; this staging step is not an approval queue.
	account := &Account{Name: in.Name, Platform: in.Platform, Type: in.Type, Credentials: SanitizeStoredCredentials(in.Platform, in.Credentials), Extra: map[string]any{}, Concurrency: max(1, int(math.Floor(float64(in.Concurrency)*in.ConcurrencyMultiplier))), RateMultiplier: &in.SellRate, Priority: 50, Status: StatusActive, Schedulable: false, AccountScope: "shared", ProxyID: proxyID}
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
	if s.health != nil {
		accountID, platform := account.ID, account.Platform
		go func() {
			defer func() { _ = recover() }()
			if err := s.health.Check(context.Background(), accountID, platform); err != nil {
				_ = s.accounts.SetError(context.Background(), accountID, "shared account health check failed: "+err.Error())
			}
		}()
	}
	return listing, nil
}
