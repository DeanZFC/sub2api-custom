package service

import (
	"context"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

var ErrSharedEarningsEmpty = infraerrors.BadRequest("SHARED_EARNINGS_EMPTY", "no shared account earnings available to transfer")

// Money crosses this boundary as decimal strings. Arithmetic stays in NUMERIC SQL.
type SharedWallet struct {
	Pending          string `json:"pending"`
	Available        string `json:"available"`
	Frozen           string `json:"frozen"`
	TotalEarned      string `json:"total_earned"`
	TotalTransferred string `json:"total_transferred"`
}
type SharedTransfer struct {
	ID           int64  `json:"id"`
	Amount       string `json:"amount"`
	BalanceAfter string `json:"balance_after"`
}
type SharedWalletRepository interface {
	GetWallet(context.Context, int64) (*SharedWallet, error)
	TransferEarnings(context.Context, int64, string) (*SharedTransfer, error)
}
type SharedWalletService struct {
	repo    SharedWalletRepository
	auth    APIKeyAuthCacheInvalidator
	billing *BillingCacheService
}

func NewSharedWalletService(repo SharedWalletRepository, auth APIKeyAuthCacheInvalidator, billing *BillingCacheService) *SharedWalletService {
	return &SharedWalletService{repo, auth, billing}
}
func (s *SharedWalletService) Wallet(ctx context.Context, userID int64) (*SharedWallet, error) {
	return s.repo.GetWallet(ctx, userID)
}
func (s *SharedWalletService) Transfer(ctx context.Context, userID int64, key string) (*SharedTransfer, error) {
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 128 {
		return nil, infraerrors.BadRequest("INVALID_TRANSFER_KEY", "Idempotency-Key must contain 8 to 128 characters")
	}
	result, err := s.repo.TransferEarnings(ctx, userID, key)
	if err != nil {
		return nil, err
	}
	if s.auth != nil {
		s.auth.InvalidateAuthCacheByUserID(ctx, userID)
	}
	if s.billing != nil {
		if err := s.billing.InvalidateUserBalance(ctx, userID); err != nil {
			logger.LegacyPrintf("service.shared_pool", "invalidate balance cache for user %d: %v", userID, err)
		}
	}
	return result, nil
}
