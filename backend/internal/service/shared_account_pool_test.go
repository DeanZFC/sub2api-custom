package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSharedAccountUploadRejectsUnsafeOrIncompleteInput(t *testing.T) {
	s := NewSharedAccountUploadService(nil, nil, nil, nil)
	for _, input := range []SharedAccountUploadInput{
		{Name: "", Platform: "openai", Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "x"}},
		{Name: "x", Platform: "openai", Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "x"}, ConcurrencyMultiplier: 6},
		{Name: "x", Platform: "openai", Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "x"}, SellRate: 101},
	} {
		_, err := s.Upload(context.Background(), 42, input)
		require.Error(t, err)
	}
}

// Embedded interfaces make any unexpected repository operation fail the test.
type sharedUploadAccounts struct {
	AccountRepository
	created     *Account
	setErrorID  int64
	setErrorMsg string
}

func (r *sharedUploadAccounts) SetError(_ context.Context, id int64, msg string) error {
	r.setErrorID = id
	r.setErrorMsg = msg
	return nil
}
func (r *sharedUploadAccounts) Create(_ context.Context, a *Account) error {
	a.ID = 7
	r.created = a
	return nil
}
func (r *sharedUploadAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.created == nil || r.created.ID != id {
		return nil, ErrSharedListingNotFound
	}
	copy := *r.created
	return &copy, nil
}
func (r *sharedUploadAccounts) Update(_ context.Context, a *Account) error {
	r.created = a
	return nil
}

type sharedUploadListings struct {
	SharedAccountPoolRepository
	created *SharedAccountListing
}

func (r *sharedUploadListings) CreateListing(_ context.Context, l *SharedAccountListing) error {
	l.ID = 8
	r.created = l
	return nil
}
func (r *sharedUploadListings) GetOwnerListingByAccountID(_ context.Context, ownerID, accountID int64) (*SharedAccountListing, error) {
	if r.created == nil || r.created.OwnerUserID != ownerID || r.created.AccountID != accountID {
		return nil, ErrSharedListingNotFound
	}
	copy := *r.created
	return &copy, nil
}
func (r *sharedUploadListings) UpdateListingMeta(_ context.Context, ownerID, listingID int64, displayName string, concurrency int, sellRate float64) error {
	if r.created == nil || r.created.OwnerUserID != ownerID || r.created.ID != listingID {
		return ErrSharedListingNotFound
	}
	r.created.DisplayName = displayName
	r.created.ConcurrencyLimit = concurrency
	r.created.SellRate = sellRate
	return nil
}
func TestSharedAccountUploadPublishesWithoutApproval(t *testing.T) {
	accounts := &sharedUploadAccounts{}
	listings := &sharedUploadListings{}
	svc := NewSharedAccountUploadService(accounts, listings, nil, nil)
	listing, err := svc.Upload(context.Background(), 42, SharedAccountUploadInput{Name: "my account", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}, Concurrency: 3, ConcurrencyMultiplier: 2, SellRate: 1.5})
	require.NoError(t, err)
	require.Equal(t, "active", listing.Status)
	require.Equal(t, int64(42), listing.OwnerUserID)
	require.Equal(t, "shared", accounts.created.AccountScope)
	require.Equal(t, StatusActive, accounts.created.Status)
	require.False(t, accounts.created.Schedulable, "repository must publish only after the listing exists")
	require.Equal(t, 6, accounts.created.Concurrency)
	require.Equal(t, 1.5, accounts.created.BillingRateMultiplier())
}

func TestSharedAccountUpdateRejectsSellRateIncreaseWhileActiveAndUsed(t *testing.T) {
	rate := 0.5
	accounts := &sharedUploadAccounts{created: &Account{ID: 7, Name: "cheap", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AccountScope: "shared", Credentials: map[string]any{"api_key": "test"}, RateMultiplier: &rate, Status: StatusActive}}
	listings := &sharedUploadListings{created: &SharedAccountListing{ID: 8, OwnerUserID: 42, AccountID: 7, Platform: PlatformOpenAI, DisplayName: "cheap", Status: "active", ConcurrencyLimit: 1, ConcurrencyMultiplier: 1, SellRate: 0.5, TotalCallCount: 12}}
	svc := NewSharedAccountUploadService(accounts, listings, nil, nil)
	higher := 5.0
	_, err := svc.UpdateOwned(context.Background(), 42, 7, SharedAccountUpdateInput{SellRate: &higher})
	require.Error(t, err)
	require.Contains(t, err.Error(), "SHARED_SELL_RATE_LOCKED")
	lower := 0.2
	updated, err := svc.UpdateOwned(context.Background(), 42, 7, SharedAccountUpdateInput{SellRate: &lower})
	require.NoError(t, err)
	require.InDelta(t, 0.2, updated.BillingRateMultiplier(), 1e-6)
}

func TestSharedAccountUpdateAllowsSellRateIncreaseAfterPause(t *testing.T) {
	rate := 0.5
	accounts := &sharedUploadAccounts{created: &Account{ID: 7, Name: "cheap", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, AccountScope: "shared", Credentials: map[string]any{"api_key": "test"}, RateMultiplier: &rate, Status: StatusActive}}
	listings := &sharedUploadListings{created: &SharedAccountListing{ID: 8, OwnerUserID: 42, AccountID: 7, Platform: PlatformOpenAI, DisplayName: "cheap", Status: "paused", ConcurrencyLimit: 1, ConcurrencyMultiplier: 1, SellRate: 0.5, TotalCallCount: 12}}
	svc := NewSharedAccountUploadService(accounts, listings, nil, nil)
	higher := 1.2
	updated, err := svc.UpdateOwned(context.Background(), 42, 7, SharedAccountUpdateInput{SellRate: &higher})
	require.NoError(t, err)
	require.InDelta(t, 1.2, updated.BillingRateMultiplier(), 1e-6)
}

func TestSharedSellRateAffectsActualDebitAndKeepsSystemPricing(t *testing.T) {
	for _, scope := range []string{"system", "shared"} {
		t.Run(scope, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			rate := 2.0
			usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 100}
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{RequestID: "shared-pricing", Model: "gpt-5.1", Usage: usage},
				APIKey: &APIKey{ID: 1}, User: &User{ID: 2}, Account: &Account{ID: 3, AccountScope: scope, RateMultiplier: &rate},
			})
			require.NoError(t, err)
			multiplier := svc.cfg.Default.RateMultiplier
			if scope == "shared" {
				multiplier *= 2
			}
			expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, multiplier)
			require.NotNil(t, billingRepo.lastCmd)
			require.InDelta(t, expected.ActualCost, billingRepo.lastCmd.BalanceCost, 1e-8)
			if scope == "shared" {
				require.NotNil(t, billingRepo.lastCmd.SharedAccountFeeRatePercent)
			} else {
				require.Nil(t, billingRepo.lastCmd.SharedAccountFeeRatePercent)
			}
		})
	}
}
func TestSharedGroupMarkerSurvivesAuthCache(t *testing.T) {
	group := &Group{ID: 9, Name: "renamed", IsSharedPool: true}
	snapshot := apiKeyAuthGroupSnapshotFromGroup(group)
	require.True(t, snapshot.IsSharedPool)
}

type sharedHealthCheckerStub struct{ checked chan int64 }

func (h *sharedHealthCheckerStub) Check(_ context.Context, id int64, _ string) error {
	h.checked <- id
	return errors.New("bad credentials")
}

func TestSharedAccountUploadDoesNotRunBackgroundHealthCheck(t *testing.T) {
	accounts := &sharedUploadAccounts{}
	listings := &sharedUploadListings{}
	health := &sharedHealthCheckerStub{checked: make(chan int64, 1)}
	svc := NewSharedAccountUploadService(accounts, listings, nil, nil)
	svc.SetHealthChecker(health)
	_, err := svc.Upload(context.Background(), 42, SharedAccountUploadInput{Name: "health", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}})
	require.NoError(t, err)
	select {
	case id := <-health.checked:
		t.Fatalf("unexpected background health check for account %d", id)
	case <-time.After(50 * time.Millisecond):
		// Upload remains active and schedulable; validation is explicit via the
		// account-management compatible test-connection action.
	}
}

type sharedProxyRepoStub struct {
	ProxyRepository
	created *Proxy
}

func (r *sharedProxyRepoStub) Create(_ context.Context, p *Proxy) error {
	p.ID = 99
	r.created = p
	return nil
}
func (r *sharedProxyRepoStub) Delete(context.Context, int64) error { return nil }

func TestSharedUploadTagsProxyWithOwner(t *testing.T) {
	accounts := &sharedUploadAccounts{}
	listings := &sharedUploadListings{}
	proxies := &sharedProxyRepoStub{}
	svc := NewSharedAccountUploadService(accounts, listings, nil, proxies)
	_, err := svc.Upload(context.Background(), 42, SharedAccountUploadInput{Name: "proxy", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}, ProxyURL: "http://user:pass@example.com:8080"})
	require.NoError(t, err)
	require.NotNil(t, proxies.created)
	require.NotNil(t, proxies.created.OwnerUserID)
	require.Equal(t, int64(42), *proxies.created.OwnerUserID)
}

func TestSharedUploadPreservesAuthorizationTypesAndFiltersAdminExtra(t *testing.T) {
	for _, input := range []SharedAccountUploadInput{
		{Name: "agent", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModeAgentIdentity, "agent_runtime_id": "runtime", "agent_private_key": "key"}},
		{Name: "pat", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModePersonalAccessToken, "access_token": "at-test"}},
		{Name: "bedrock", Platform: PlatformAnthropic, Type: AccountTypeBedrock, Credentials: map[string]any{"auth_mode": "sigv4", "aws_access_key_id": "key", "aws_secret_access_key": "secret", "aws_region": "us-east-1"}},
		{Name: "vertex", Platform: PlatformGemini, Type: AccountTypeServiceAccount, Credentials: map[string]any{"service_account_json": "{}", "project_id": "project", "location": "global"}},
	} {
		t.Run(input.Name, func(t *testing.T) {
			accounts := &sharedUploadAccounts{}
			listings := &sharedUploadListings{}
			svc := NewSharedAccountUploadService(accounts, listings, nil, nil)
			input.Concurrency = 4
			input.SellRate = 1.6
			input.Extra = map[string]any{"email": "test@example.com", "openai_passthrough": true, "quota_limit": 100}
			expiry := time.Now().Add(time.Hour)
			input.ExpiresAt = &expiry
			listing, err := svc.Upload(context.Background(), 42, input)
			require.NoError(t, err)
			require.Equal(t, "active", listing.Status)
			require.Equal(t, int64(42), listing.OwnerUserID)
			require.Equal(t, "shared", accounts.created.AccountScope)
			require.Equal(t, input.Type, accounts.created.Type)
			require.Equal(t, input.Credentials, accounts.created.Credentials)
			require.Equal(t, 4, accounts.created.Concurrency)
			require.Equal(t, 1.6, accounts.created.BillingRateMultiplier())
			require.Equal(t, &expiry, accounts.created.ExpiresAt)
			require.True(t, accounts.created.AutoPauseOnExpired)
			require.Equal(t, map[string]any{"email": "test@example.com"}, accounts.created.Extra)
		})
	}
}
