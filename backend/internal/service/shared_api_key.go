package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	SharedKeySelectionManual      = "manual"
	SharedKeySelectionPlatform    = "platform"
	SharedKeyPriorityOrder        = "order"
	SharedKeyPriorityRate         = "rate"
	SharedKeyPriorityAvailability = "availability"
)

type SharedAPIKey struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	Name              string    `json:"name"`
	Key               string    `json:"key,omitempty"`
	KeyPreview        string    `json:"key_preview"`
	Platform          string    `json:"platform"`
	Status            string    `json:"status"`
	SelectionMode     string    `json:"selection_mode"`
	PriorityMode      string    `json:"priority_mode"`
	ListingIDs        []int64   `json:"listing_ids"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LegacyAPIKeyID    int64     `json:"-"`
	User              *User     `json:"-"`
	Group             *Group    `json:"-"`
	ListingAccountIDs []int64   `json:"-"`
	previousKey       string
}

// SetPreviousKeyForRotation is restricted to repository rotation plumbing.
func (k *SharedAPIKey) SetPreviousKeyForRotation(previous string) {
	if k != nil {
		k.previousKey = previous
	}
}

type SharedAPIKeyRepository interface {
	Create(ctx context.Context, key *SharedAPIKey) error
	ListByUser(ctx context.Context, userID int64) ([]SharedAPIKey, error)
	GetByID(ctx context.Context, userID, id int64) (*SharedAPIKey, error)
	GetByKey(ctx context.Context, raw string) (*SharedAPIKey, error)
	Update(ctx context.Context, key *SharedAPIKey) error
	Delete(ctx context.Context, userID, id int64) error
	// RotateKey atomically replaces the shared key and its legacy api_keys
	// compatibility row. The returned value contains the newly generated key.
	RotateKey(ctx context.Context, userID, id int64, newKey string) (*SharedAPIKey, error)
}

type SharedAPIKeyAbuseRepository interface {
	CountByUser(ctx context.Context, userID int64) (active int, recent int, err error)
}

// SharedAPIKeyCreateLock serializes the per-user quota check with key
// creation. Without it, concurrent requests can all observe the same count
// and exceed the active/hourly limits.
type SharedAPIKeyCreateLock interface {
	WithUserCreateLock(ctx context.Context, userID int64, fn func() error) error
}

var ErrSharedAPIKeyNotFound = errors.New("shared api key not found")

type SharedAPIKeyService struct {
	repo      SharedAPIKeyRepository
	users     UserRepository
	groups    GroupRepository
	authCache SharedAPIKeyAuthCacheInvalidator
}

type SharedAPIKeyAuthCacheInvalidator interface {
	InvalidateAuthCacheByKey(ctx context.Context, key string)
}

func NewSharedAPIKeyService(repo SharedAPIKeyRepository, deps ...any) *SharedAPIKeyService {
	s := &SharedAPIKeyService{repo: repo}
	for _, d := range deps {
		switch v := d.(type) {
		case UserRepository:
			s.users = v
		case GroupRepository:
			s.groups = v
		case SharedAPIKeyAuthCacheInvalidator:
			s.authCache = v
		}
	}
	return s
}

func generateSharedAPIKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sk-shared-" + hex.EncodeToString(b), nil
}
func normalizeSharedKeyModes(selection, priority string) (string, string, error) {
	selection = strings.ToLower(strings.TrimSpace(selection))
	priority = strings.ToLower(strings.TrimSpace(priority))
	if selection == "" {
		selection = SharedKeySelectionManual
	}
	if priority == "" {
		priority = SharedKeyPriorityOrder
	}
	switch selection {
	case SharedKeySelectionManual, SharedKeySelectionPlatform:
	default:
		return "", "", errors.New("invalid selection mode")
	}
	switch priority {
	case SharedKeyPriorityOrder, SharedKeyPriorityRate, SharedKeyPriorityAvailability:
	default:
		return "", "", errors.New("invalid priority mode")
	}
	if selection == SharedKeySelectionPlatform && priority == SharedKeyPriorityOrder {
		priority = SharedKeyPriorityRate
	}
	return selection, priority, nil
}

func normalizeSharedListings(listings []int64) ([]int64, error) {
	if len(listings) > 20 {
		return nil, errors.New("a shared API key can bind at most 20 accounts")
	}
	seen := make(map[int64]struct{}, len(listings))
	unique := make([]int64, 0, len(listings))
	for _, id := range listings {
		if id <= 0 {
			return nil, errors.New("invalid shared listing id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique, nil
}

func (s *SharedAPIKeyService) Create(ctx context.Context, userID int64, name, platform, selection, priority string, listings []int64) (*SharedAPIKey, error) {
	if lock, ok := s.repo.(SharedAPIKeyCreateLock); ok {
		var result *SharedAPIKey
		err := lock.WithUserCreateLock(ctx, userID, func() error {
			var err error
			result, err = s.createUnlocked(ctx, userID, name, platform, selection, priority, listings)
			return err
		})
		return result, err
	}
	return s.createUnlocked(ctx, userID, name, platform, selection, priority, listings)
}

func (s *SharedAPIKeyService) createUnlocked(ctx context.Context, userID int64, name, platform, selection, priority string, listings []int64) (*SharedAPIKey, error) {
	if userID <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(platform) == "" {
		return nil, errors.New("name and platform are required")
	}
	if abuse, ok := s.repo.(SharedAPIKeyAbuseRepository); ok {
		active, recent, err := abuse.CountByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		if active >= 20 {
			return nil, errors.New("shared API key limit reached")
		}
		if recent >= 10 {
			return nil, errors.New("too many shared API key creations; try again later")
		}
	}
	selection, priority, err := normalizeSharedKeyModes(selection, priority)
	if err != nil {
		return nil, err
	}
	unique, err := normalizeSharedListings(listings)
	if err != nil {
		return nil, err
	}
	if selection == SharedKeySelectionManual && len(unique) == 0 {
		return nil, errors.New("name, platform and at least one listing are required")
	}
	if selection == SharedKeySelectionPlatform {
		unique = nil
	}
	key, err := generateSharedAPIKey()
	if err != nil {
		return nil, err
	}
	v := &SharedAPIKey{UserID: userID, Name: strings.TrimSpace(name), Platform: strings.ToLower(strings.TrimSpace(platform)), Key: key, Status: StatusAPIKeyActive, SelectionMode: selection, PriorityMode: priority, ListingIDs: unique}
	if err := s.repo.Create(ctx, v); err != nil {
		return nil, err
	}
	v.KeyPreview = previewSharedAPIKey(v.Key)
	return v, nil
}

func previewSharedAPIKey(key string) string {
	if len(key) <= 10 {
		return key
	}
	return key[:6] + "…" + key[len(key)-4:]
}
func (s *SharedAPIKeyService) List(ctx context.Context, userID int64) ([]SharedAPIKey, error) {
	items, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Lists expose only a preview. The owner can retrieve the complete value
	// through the ownership-checked secret endpoint when copying or using it.
	for i := range items {
		items[i].Key = ""
	}
	return items, nil
}
func (s *SharedAPIKeyService) GetByID(ctx context.Context, userID, id int64) (*SharedAPIKey, error) {
	return s.repo.GetByID(ctx, userID, id)
}
func (s *SharedAPIKeyService) GetByKey(ctx context.Context, key string) (*SharedAPIKey, error) {
	v, e := s.repo.GetByKey(ctx, strings.TrimSpace(key))
	if e != nil {
		return nil, e
	}
	if s.users != nil {
		v.User, _ = s.users.GetByID(ctx, v.UserID)
	}
	if s.groups != nil {
		gs, _ := s.groups.ListActiveByPlatform(ctx, v.Platform)
		for i := range gs {
			if gs[i].IsSharedPool {
				v.Group = &gs[i]
				break
			}
		}
	}
	return v, nil
}
func (s *SharedAPIKeyService) Update(ctx context.Context, userID, id int64, name, status, platform, selection, priority string, listings []int64) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("name is required")
	}
	selection, priority, err := normalizeSharedKeyModes(selection, priority)
	if err != nil {
		return err
	}
	unique, err := normalizeSharedListings(listings)
	if err != nil {
		return err
	}
	if selection == SharedKeySelectionManual && len(unique) == 0 {
		return errors.New("name and listings are required")
	}
	if selection == SharedKeySelectionPlatform {
		unique = nil
	}
	k, err := s.repo.GetByID(ctx, userID, id)
	if err != nil {
		return err
	}
	k.Name = strings.TrimSpace(name)
	if strings.TrimSpace(platform) != "" {
		k.Platform = strings.ToLower(strings.TrimSpace(platform))
	}
	k.SelectionMode = selection
	k.PriorityMode = priority
	if status != "" && status != StatusAPIKeyActive && status != StatusAPIKeyDisabled {
		return errors.New("invalid shared API key status")
	}
	if status != "" {
		k.Status = status
	}
	k.ListingIDs = unique
	return s.repo.Update(ctx, k)
}
func (s *SharedAPIKeyService) Delete(ctx context.Context, userID, id int64) error {
	key, err := s.repo.GetByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if err = s.repo.Delete(ctx, userID, id); err != nil {
		return err
	}
	if s.authCache != nil && key != nil && key.Key != "" {
		s.authCache.InvalidateAuthCacheByKey(ctx, key.Key)
	}
	return nil
}

// Rotate generates a fresh credential and invalidates the previous one in a
// single database transaction. The new key is returned to the owner.
func (s *SharedAPIKeyService) Rotate(ctx context.Context, userID, id int64) (*SharedAPIKey, error) {
	if userID <= 0 || id <= 0 {
		return nil, ErrSharedAPIKeyNotFound
	}
	newKey, err := generateSharedAPIKey()
	if err != nil {
		return nil, err
	}
	v, err := s.repo.RotateKey(ctx, userID, id, newKey)
	if err == nil && s.authCache != nil {
		if v.previousKey != "" {
			s.authCache.InvalidateAuthCacheByKey(ctx, v.previousKey)
		}
		s.authCache.InvalidateAuthCacheByKey(ctx, newKey)
	}
	return v, err
}

type sharedListingOrderKey struct{}
type sharedKeyScheduleKey struct{}

type SharedKeySchedule struct {
	Priority   string
	AccountIDs []int64
}

func WithSharedListingOrder(ctx context.Context, ids []int64) context.Context {
	return WithSharedKeySchedule(ctx, SharedKeySchedule{Priority: SharedKeyPriorityOrder, AccountIDs: ids})
}

func WithSharedKeySchedule(ctx context.Context, schedule SharedKeySchedule) context.Context {
	ctx = context.WithValue(ctx, sharedKeyScheduleKey{}, schedule)
	return context.WithValue(ctx, sharedListingOrderKey{}, schedule.AccountIDs)
}

func SharedListingOrder(ctx context.Context) []int64 {
	if schedule, ok := ctx.Value(sharedKeyScheduleKey{}).(SharedKeySchedule); ok {
		return schedule.AccountIDs
	}
	v, _ := ctx.Value(sharedListingOrderKey{}).([]int64)
	return v
}

func SharedKeyScheduleFrom(ctx context.Context) SharedKeySchedule {
	if schedule, ok := ctx.Value(sharedKeyScheduleKey{}).(SharedKeySchedule); ok {
		return schedule
	}
	return SharedKeySchedule{Priority: SharedKeyPriorityOrder, AccountIDs: SharedListingOrder(ctx)}
}

func IsSharedPoolSchedule(ctx context.Context) bool {
	_, ok := ctx.Value(sharedKeyScheduleKey{}).(SharedKeySchedule)
	return ok
}

func applySharedKeySchedule(ctx context.Context, accounts []Account) []Account {
	if len(accounts) == 0 {
		return accounts
	}
	schedule := SharedKeyScheduleFrom(ctx)
	priority := strings.ToLower(strings.TrimSpace(schedule.Priority))
	if priority == "" {
		priority = SharedKeyPriorityOrder
	}
	ordered := accounts
	if len(schedule.AccountIDs) > 0 {
		ordered = applySharedListingIDs(accounts, schedule.AccountIDs)
	}
	switch priority {
	case SharedKeyPriorityRate:
		return sortSharedAccountsByRate(ordered, schedule.AccountIDs)
	case SharedKeyPriorityAvailability:
		return sortSharedAccountsByAvailability(ordered, schedule.AccountIDs)
	default:
		return ordered
	}
}

func applySharedListingIDs(accounts []Account, ids []int64) []Account {
	if len(ids) == 0 {
		return accounts
	}
	byID := make(map[int64]Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	out := make([]Account, 0, len(accounts))
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			out = append(out, a)
		}
	}
	return out
}

func sortSharedAccountsByRate(accounts []Account, preferred []int64) []Account {
	return sortSharedAccounts(accounts, preferred, func(a, b Account) bool {
		ar, br := a.BillingRateMultiplier(), b.BillingRateMultiplier()
		if ar != br {
			return ar < br
		}
		return a.ID < b.ID
	})
}

func sortSharedAccountsByAvailability(accounts []Account, preferred []int64) []Account {
	now := time.Now()
	return sortSharedAccounts(accounts, preferred, func(a, b Account) bool {
		ab, bb := sharedAccountBusy(a, now), sharedAccountBusy(b, now)
		if ab != bb {
			return !ab
		}
		if a.LastUsedAt == nil && b.LastUsedAt != nil {
			return true
		}
		if a.LastUsedAt != nil && b.LastUsedAt == nil {
			return false
		}
		if a.LastUsedAt != nil && b.LastUsedAt != nil && !a.LastUsedAt.Equal(*b.LastUsedAt) {
			return a.LastUsedAt.Before(*b.LastUsedAt)
		}
		return a.ID < b.ID
	})
}

func sortSharedAccounts(accounts []Account, preferred []int64, less func(Account, Account) bool) []Account {
	if len(accounts) < 2 {
		return accounts
	}
	preferredSet := make(map[int64]bool, len(preferred))
	for _, id := range preferred {
		preferredSet[id] = true
	}
	out := append([]Account(nil), accounts...)
	sort.SliceStable(out, func(i, j int) bool {
		if len(preferredSet) > 0 {
			ip, jp := preferredSet[out[i].ID], preferredSet[out[j].ID]
			if ip != jp {
				return ip
			}
		}
		return less(out[i], out[j])
	})
	return out
}

func sharedAccountBusy(a Account, now time.Time) bool {
	if a.TempUnschedulableUntil != nil && a.TempUnschedulableUntil.After(now) {
		return true
	}
	if a.RateLimitResetAt != nil && a.RateLimitResetAt.After(now) {
		return true
	}
	if a.OverloadUntil != nil && a.OverloadUntil.After(now) {
		return true
	}
	return false
}
