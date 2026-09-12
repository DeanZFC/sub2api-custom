package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type SharedAPIKey struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	Name              string    `json:"name"`
	Key               string    `json:"key,omitempty"`
	KeyPreview        string    `json:"key_preview"`
	Platform          string    `json:"platform"`
	Status            string    `json:"status"`
	ListingIDs        []int64   `json:"listing_ids"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	LegacyAPIKeyID    int64     `json:"-"`
	User              *User     `json:"-"`
	Group             *Group    `json:"-"`
	ListingAccountIDs []int64   `json:"-"`
}

type SharedAPIKeyRepository interface {
	Create(ctx context.Context, key *SharedAPIKey) error
	ListByUser(ctx context.Context, userID int64) ([]SharedAPIKey, error)
	GetByID(ctx context.Context, userID, id int64) (*SharedAPIKey, error)
	GetByKey(ctx context.Context, raw string) (*SharedAPIKey, error)
	Update(ctx context.Context, key *SharedAPIKey) error
	Delete(ctx context.Context, userID, id int64) error
}

var ErrSharedAPIKeyNotFound = errors.New("shared api key not found")

type SharedAPIKeyService struct {
	repo   SharedAPIKeyRepository
	users  UserRepository
	groups GroupRepository
}

func NewSharedAPIKeyService(repo SharedAPIKeyRepository, deps ...any) *SharedAPIKeyService {
	s := &SharedAPIKeyService{repo: repo}
	for _, d := range deps {
		switch v := d.(type) {
		case UserRepository:
			s.users = v
		case GroupRepository:
			s.groups = v
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
func (s *SharedAPIKeyService) Create(ctx context.Context, userID int64, name, platform string, listings []int64) (*SharedAPIKey, error) {
	if userID <= 0 || strings.TrimSpace(name) == "" || strings.TrimSpace(platform) == "" || len(listings) == 0 {
		return nil, errors.New("name, platform and at least one listing are required")
	}
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
	if len(unique) == 0 {
		return nil, errors.New("at least one shared account is required")
	}
	key, err := generateSharedAPIKey()
	if err != nil {
		return nil, err
	}
	v := &SharedAPIKey{UserID: userID, Name: strings.TrimSpace(name), Platform: strings.ToLower(strings.TrimSpace(platform)), Key: key, Status: StatusAPIKeyActive, ListingIDs: unique}
	if err := s.repo.Create(ctx, v); err != nil {
		return nil, err
	}
	return v, nil
}
func (s *SharedAPIKeyService) List(ctx context.Context, userID int64) ([]SharedAPIKey, error) {
	return s.repo.ListByUser(ctx, userID)
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
func (s *SharedAPIKeyService) Update(ctx context.Context, userID, id int64, name, status string, listings []int64) error {
	if name == "" || len(listings) == 0 || len(listings) > 20 {
		return errors.New("name and listings are required")
	}
	k, err := s.repo.GetByID(ctx, userID, id)
	if err != nil {
		return err
	}
	k.Name = strings.TrimSpace(name)
	if status != "" && status != StatusAPIKeyActive && status != StatusAPIKeyDisabled {
		return errors.New("invalid shared API key status")
	}
	if status != "" {
		k.Status = status
	}
	seen := make(map[int64]struct{}, len(listings))
	k.ListingIDs = make([]int64, 0, len(listings))
	for _, listingID := range listings {
		if listingID <= 0 {
			return errors.New("invalid shared listing id")
		}
		if _, ok := seen[listingID]; ok {
			continue
		}
		seen[listingID] = struct{}{}
		k.ListingIDs = append(k.ListingIDs, listingID)
	}
	return s.repo.Update(ctx, k)
}
func (s *SharedAPIKeyService) Delete(ctx context.Context, userID, id int64) error {
	return s.repo.Delete(ctx, userID, id)
}

type sharedListingOrderKey struct{}

func WithSharedListingOrder(ctx context.Context, ids []int64) context.Context {
	return context.WithValue(ctx, sharedListingOrderKey{}, ids)
}
func SharedListingOrder(ctx context.Context) []int64 {
	v, _ := ctx.Value(sharedListingOrderKey{}).([]int64)
	return v
}
