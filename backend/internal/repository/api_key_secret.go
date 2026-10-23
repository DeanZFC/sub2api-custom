package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/apikey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *apiKeyRepository) RotateSecret(ctx context.Context, id, userID int64, previous, raw string) error {
	n, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.UserIDEQ(userID), apikey.KeyEQ(previous), apikey.DeletedAtIsNil()).
		SetKey(service.APIKeyStoredValue(raw)).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return translatePersistenceError(err, nil, service.ErrAPIKeyExists)
	}
	if n != 1 {
		return infraerrors.Conflict("API_KEY_CHANGED", "API key changed; refresh before regenerating")
	}
	return nil
}

func (r *apiKeyRepository) CredentialMatches(ctx context.Context, id int64, raw string) (bool, error) {
	return r.activeQuery().Where(apikey.IDEQ(id), apikey.KeyEQ(service.APIKeyStoredValue(raw))).Exist(ctx)
}
