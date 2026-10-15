package service

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
)

func ProvideQQBotService(db *sql.DB, encryptor SecretEncryptor, cfg *config.Config, invalidator APIKeyAuthCacheInvalidator, billing *BillingCacheService, email *EmailService, settings *SettingService, users UserRepository) *qqbot.Service {
	s := qqbot.New(db, encryptor, cfg.Totp.EncryptionKeyConfigured, func(ctx context.Context, user int64) {
		if invalidator != nil {
			invalidator.InvalidateAuthCacheByUserID(ctx, user)
		}
		if billing != nil {
			_ = billing.InvalidateUserBalance(ctx, user)
		}
	})
	s.SetBindingEmailDelivery(&qqBotBindingEmail{email: email, settings: settings, users: users})
	s.Start()
	return s
}

type qqBotBindingEmail struct {
	email    *EmailService
	settings *SettingService
	users    UserRepository
}

func (d *qqBotBindingEmail) FrontendURL(ctx context.Context) string {
	return d.settings.GetFrontendURL(ctx)
}

func (d *qqBotBindingEmail) LookupAccount(ctx context.Context, address string) (int64, string, error) {
	u, err := d.users.GetByEmail(ctx, address)
	if err != nil {
		return 0, "", err
	}
	if u == nil || !u.IsActive() || u.DeletedAt != nil {
		return 0, "", ErrUserNotFound
	}
	return u.ID, u.Email, nil
}

func (d *qqBotBindingEmail) SendEmail(ctx context.Context, to, subject, body string) error {
	return d.email.SendEmail(ctx, to, subject, body)
}
