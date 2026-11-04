package service

import "github.com/Wei-Shaw/sub2api/internal/config"

// The credential worker stores only an independent Excel grant. It cannot
// replace the account's primary OAuth credentials or scheduling settings.
func ProvideOpenAIOAuthReauthService(
	repo OpenAIOAuthReauthRepository,
	adminService AdminService,
	oauth *OpenAIOAuthService,
	encryptor OpenAICredentialEncryptor,
	cfg *config.Config,
	tokenCacheInvalidator TokenCacheInvalidator,
	runtimeBlocker AccountRuntimeBlocker,
	buildInfo BuildInfo,
	settings SettingRepository,
) *OpenAIOAuthReauthService {
	configured := cfg != nil && cfg.Totp.EncryptionKeyConfigured
	svc := NewOpenAIOAuthReauthService(repo, adminService, nil, oauth, encryptor, configured, tokenCacheInvalidator, runtimeBlocker)
	svc.settings = settings
	svc.configureWorker(cfg, buildInfo)
	if admin, ok := adminService.(*adminServiceImpl); ok {
		admin.excelOAuthReauth = svc
	}
	if gateway, ok := runtimeBlocker.(*OpenAIGatewayService); ok {
		gateway.excelOAuthReauth = svc
	}
	svc.Start()
	if gateway, ok := runtimeBlocker.(*OpenAIGatewayService); ok {
		gateway.StartBPS403Recovery()
	}
	return svc
}
