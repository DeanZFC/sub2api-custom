package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type reauthTestEncryptor struct{}

func (reauthTestEncryptor) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (reauthTestEncryptor) Decrypt(value string) (string, error) {
	plain, ok := strings.CutPrefix(value, "encrypted:")
	if !ok {
		return "", errors.New("invalid ciphertext")
	}
	return plain, nil
}

type reauthTestAccountReader struct {
	account  *Account
	proxy    *Proxy
	proxyErr error
}

func (r *reauthTestAccountReader) GetAccount(context.Context, int64) (*Account, error) {
	if r.account == nil {
		return nil, ErrAccountNotFound
	}
	copy := *r.account
	copy.Credentials = cloneReauthMap(r.account.Credentials)
	return &copy, nil
}
func (r *reauthTestAccountReader) GetProxy(context.Context, int64) (*Proxy, error) {
	return r.proxy, r.proxyErr
}

type reauthTestProxyRepo struct {
	ProxyRepository
	reader *reauthTestAccountReader
}

func (r *reauthTestProxyRepo) GetByID(ctx context.Context, id int64) (*Proxy, error) {
	return r.reader.GetProxy(ctx, id)
}

type reauthTestOAuthClient struct {
	accountID  string
	userID     string
	email      string
	exchanges  int
	proxyURL   string
	onExchange func()
}

func (c *reauthTestOAuthClient) ExchangeCode(_ context.Context, _, _, _, proxyURL, _ string) (*openai.TokenResponse, error) {
	if c.onExchange != nil {
		c.onExchange()
	}
	c.exchanges++
	c.proxyURL = proxyURL
	return &openai.TokenResponse{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		IDToken: reauthTestJWT(map[string]any{
			"email": c.email,
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": c.accountID,
				"chatgpt_user_id":    c.userID,
			},
		}),
		ExpiresIn: 3600,
	}, nil
}
func (*reauthTestOAuthClient) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not used")
}
func (*reauthTestOAuthClient) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not used")
}

type reauthTestRepo struct {
	config        *OpenAIOAuthReauthStoredConfig
	task          *OpenAIOAuthReauthTaskRecord
	markSucceeded int
	createCalls   int
	createErr     error
}

func (r *reauthTestRepo) UpsertConfig(_ context.Context, config *OpenAIOAuthReauthStoredConfig) error {
	copy := *config
	r.config = &copy
	return nil
}

func reauthEmailConfig(accountID int64, email, ciphertext string) *OpenAIOAuthReauthStoredConfig {
	return &OpenAIOAuthReauthStoredConfig{
		AccountID: accountID, LoginEmail: email, CredentialMode: OpenAIOAuthReauthModeEmailOTPURL,
		OTPURLCiphertext: ciphertext, UpdatedAt: time.Now(),
	}
}
func (r *reauthTestRepo) GetConfig(context.Context, int64) (*OpenAIOAuthReauthStoredConfig, error) {
	return r.config, nil
}
func (r *reauthTestRepo) CreateTask(_ context.Context, accountID int64, hash string) (*OpenAIOAuthReauthTaskRecord, error) {
	r.createCalls++
	if r.createErr != nil {
		return nil, r.createErr
	}
	r.task = &OpenAIOAuthReauthTaskRecord{ID: 71, AccountID: accountID, Status: OpenAIOAuthReauthStatusQueued, Stage: OpenAIOAuthReauthStageQueued, ExpectedCredentialsHash: hash, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	return r.task, nil
}
func (r *reauthTestRepo) GetLatestTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error) {
	return r.task, nil
}
func (r *reauthTestRepo) GetTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error) {
	return r.task, nil
}
func (r *reauthTestRepo) ClaimNextTask(_ context.Context, workerID string, _ time.Duration) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.task == nil || r.task.Status != OpenAIOAuthReauthStatusQueued {
		return nil, nil
	}
	r.task.Status = OpenAIOAuthReauthStatusRunning
	r.task.Stage = OpenAIOAuthReauthStageStarting
	r.task.WorkerID = workerID
	r.task.Attempt++
	return r.task, nil
}
func (r *reauthTestRepo) ClaimNextTaskForEngines(ctx context.Context, workerID string, stale time.Duration, mode string, engines []string) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.config == nil || (mode != "" && r.config.CredentialMode != mode) {
		return nil, nil
	}
	for _, engine := range engines {
		if normalizedReauthEngine(r.config.Engine) == engine {
			return r.ClaimNextTask(ctx, workerID, stale)
		}
	}
	return nil, nil
}

func (r *reauthTestRepo) SetSession(_ context.Context, _ int64, workerID, sessionID string) error {
	if r.task == nil || r.task.WorkerID != workerID {
		return errors.New("worker mismatch")
	}
	r.task.AuthSessionID = sessionID
	return nil
}
func (r *reauthTestRepo) UpdateStage(_ context.Context, _ int64, workerID, stage string) error {
	if r.task == nil || r.task.WorkerID != workerID {
		return errors.New("worker mismatch")
	}
	r.task.Stage = stage
	return nil
}
func (r *reauthTestRepo) BeginCallback(_ context.Context, _ int64, workerID string) (*OpenAIOAuthReauthTaskRecord, bool, error) {
	if r.task == nil || r.task.WorkerID != workerID || r.task.Status != OpenAIOAuthReauthStatusRunning || r.task.AuthSessionID == "" {
		return nil, false, errors.New("task not active")
	}
	r.task.Status = OpenAIOAuthReauthStatusCallbackProcessing
	return r.task, true, nil
}
func (r *reauthTestRepo) BeginDirectCallback(_ context.Context, _ int64, workerID string) (*OpenAIOAuthReauthTaskRecord, bool, error) {
	if r.task == nil || r.task.WorkerID != workerID || r.task.Status != OpenAIOAuthReauthStatusRunning {
		return nil, false, errors.New("task not active")
	}
	r.task.Status = OpenAIOAuthReauthStatusCallbackProcessing
	r.task.Stage = OpenAIOAuthReauthStageExchangingToken
	return r.task, true, nil
}
func (r *reauthTestRepo) MarkSucceeded(_ context.Context, _ int64, workerID string) error {
	r.markSucceeded++
	if r.task == nil || r.task.WorkerID != workerID || r.task.Status != OpenAIOAuthReauthStatusCallbackProcessing {
		return errors.New("task not active")
	}
	r.task.Status = OpenAIOAuthReauthStatusSucceeded
	r.task.Stage = OpenAIOAuthReauthStageSucceeded
	return nil
}
func (r *reauthTestRepo) MarkFailed(_ context.Context, _ int64, workerID, reason string) error {
	if r.task == nil || r.task.WorkerID != workerID {
		return errors.New("worker mismatch")
	}
	r.task.Status = OpenAIOAuthReauthStatusFailed
	r.task.Stage = OpenAIOAuthReauthStageFailed
	r.task.Error = reason
	return nil
}

type reauthTestUpdater struct {
	repo        *reauthTestRepo
	applied     bool
	credentials map[string]any
}

type reauthTestRuntimeBlocker struct {
	clearedAccountID int64
}

func (*reauthTestRuntimeBlocker) BlockAccountScheduling(*Account, time.Time, string) {}
func (b *reauthTestRuntimeBlocker) ClearAccountSchedulingBlock(accountID int64) {
	b.clearedAccountID = accountID
}

func (u *reauthTestUpdater) ApplyOpenAIOAuthReauth(_ context.Context, taskID int64, workerID string, accountID int64, _ map[string]any, credentials, _ map[string]any) (bool, error) {
	if u.repo.task == nil || u.repo.task.ID != taskID || u.repo.task.AccountID != accountID || u.repo.task.WorkerID != workerID || u.repo.task.Status != OpenAIOAuthReauthStatusCallbackProcessing {
		return false, nil
	}
	u.applied = true
	u.credentials = cloneReauthMap(credentials)
	u.repo.task.Status = OpenAIOAuthReauthStatusSucceeded
	u.repo.task.Stage = OpenAIOAuthReauthStageSucceeded
	return true, nil
}

func newReauthTestService(tokenAccountID string) (*OpenAIOAuthReauthService, *reauthTestAccountReader, *reauthTestRepo, *reauthTestUpdater, *reauthTestOAuthClient, *reauthTestRuntimeBlocker) {
	reader := &reauthTestAccountReader{account: &Account{
		ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "old-access", "refresh_token": "old-refresh",
			"chatgpt_account_id": "acct-1", "email": "user@example.com",
			"chatgpt_user_id": "user-1",
			"custom_setting":  "preserved",
		},
	}}
	repo := &reauthTestRepo{}
	updater := &reauthTestUpdater{repo: repo}
	oauthClient := &reauthTestOAuthClient{accountID: tokenAccountID, userID: "user-1", email: "user@example.com"}
	runtimeBlocker := &reauthTestRuntimeBlocker{}
	oauthService := NewOpenAIOAuthService(&reauthTestProxyRepo{reader: reader}, oauthClient)
	return NewOpenAIOAuthReauthService(repo, reader, updater, oauthService, reauthTestEncryptor{}, true, nil, runtimeBlocker), reader, repo, updater, oauthClient, runtimeBlocker
}

func savePasswordReauthConfig(t *testing.T, svc *OpenAIOAuthReauthService) {
	t.Helper()
	_, err := svc.SaveCredentialConfig(context.Background(), 42, OpenAIOAuthReauthConfigInput{
		LoginEmail: "user@example.com", CredentialMode: OpenAIOAuthReauthModePasswordTOTP,
		Password: "password-secret", TOTPSecret: "totp-secret",
	})
	require.NoError(t, err)
}

func reauthTestJWT(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func cloneReauthMap(source map[string]any) map[string]any {
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
