package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const apiKeyDigestPrefix = "$sha256$"

// APIKeyStoredValue is not a credential: authentication always hashes the input.
func APIKeyStoredValue(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return apiKeyDigestPrefix + hex.EncodeToString(sum[:]) + "$" + MaskAPIKey(raw)
}

func APIKeyCacheDigest(key string) string {
	if strings.HasPrefix(key, apiKeyDigestPrefix) && len(key) >= len(apiKeyDigestPrefix)+65 {
		return key[len(apiKeyDigestPrefix) : len(apiKeyDigestPrefix)+64]
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func MaskAPIKey(key string) string {
	if strings.HasPrefix(key, apiKeyDigestPrefix) && len(key) >= len(apiKeyDigestPrefix)+65 {
		return key[len(apiKeyDigestPrefix)+65:]
	}
	characters := []rune(key)
	if len(characters) <= 12 {
		return "***"
	}
	return string(characters[:6]) + "..." + string(characters[len(characters)-4:])
}

type apiKeySecretRepository interface {
	RotateSecret(context.Context, int64, int64, string, string) error
	CredentialMatches(context.Context, int64, string) (bool, error)
}

func (s *APIKeyService) Regenerate(ctx context.Context, id, userID int64) (*APIKey, error) {
	key, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	repo, ok := s.apiKeyRepo.(apiKeySecretRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("API_KEY_ROTATION_UNAVAILABLE", "API key regeneration is unavailable")
	}
	secret, err := s.GenerateKey()
	if err != nil {
		return nil, err
	}
	old := key.Key
	if err := repo.RotateSecret(ctx, id, userID, old, secret); err != nil {
		return nil, err
	}
	s.InvalidateAuthCacheByKey(ctx, old)
	s.InvalidateAuthCacheByKey(ctx, secret)
	key.Key = secret
	return key, nil
}

// A cache snapshot may outlive a rotation or be repopulated by an in-flight
// lookup. Check the indexed credential before accepting a cached identity.
func (s *APIKeyService) validateCachedCredential(ctx context.Context, id int64, raw string) error {
	if repo, ok := s.apiKeyRepo.(apiKeySecretRepository); ok {
		if s.authLookupSlots != nil {
			select {
			case s.authLookupSlots <- struct{}{}:
				defer func() { <-s.authLookupSlots }()
			default:
				return ErrAPIKeyAuthOverloaded
			}
		}
		matches, err := repo.CredentialMatches(ctx, id, raw)
		if err != nil {
			return err
		}
		if !matches {
			return ErrAPIKeyNotFound
		}
	}
	return nil
}

// GetForOwnerAuth is only for JWT-authenticated panel operations. It cannot be
// used on the public gateway and never reconstructs a credential.
func (s *APIKeyService) GetForOwnerAuth(ctx context.Context, id, userID int64) (*APIKey, error) {
	repo, ok := s.apiKeyRepo.(interface {
		GetByIDForAuth(context.Context, int64) (*APIKey, error)
	})
	if !ok {
		return nil, ErrAPIKeyNotFound
	}
	key, err := repo.GetByIDForAuth(ctx, id)
	if err != nil {
		return nil, err
	}
	if key == nil || key.UserID != userID {
		return nil, ErrAPIKeyNotFound
	}
	s.compileAPIKeyIPRules(key)
	return key, nil
}
