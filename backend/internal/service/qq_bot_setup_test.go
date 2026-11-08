package service

import (
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type qqSetupUsers struct {
	UserRepository
	user *User
}

func (r qqSetupUsers) GetByID(context.Context, int64) (*User, error) { return r.user, nil }

type qqSetupGroups struct {
	GroupRepository
	group *Group
}

func (r qqSetupGroups) GetByID(context.Context, int64) (*Group, error) { return r.group, nil }

type qqSetupKeys struct {
	APIKeyRepository
	key *APIKey
}

func (r qqSetupKeys) GetByID(context.Context, int64) (*APIKey, error) { return r.key, nil }
func setupProvider(key *APIKey) *qqBotSiteAI {
	users := qqSetupUsers{user: &User{ID: 7, Status: StatusActive}}
	groups := qqSetupGroups{group: &Group{ID: 2, Status: StatusActive, Platform: PlatformOpenAI}}
	keys := &APIKeyService{userRepo: users, groupRepo: groups, apiKeyRepo: qqSetupKeys{key: key}, cfg: &config.Config{}}
	return &qqBotSiteAI{users: users, groups: groups, keys: keys, baseURL: "http://127.0.0.1:8080/v1"}
}
func TestQQBotManagedKeyReuseNeverResetsSpending(t *testing.T) {
	group := int64(2)
	key := &APIKey{ID: 11, UserID: 7, GroupID: &group, Key: APIKeyStoredValue("raw-secret"), Status: StatusActive, Quota: 20, QuotaUsed: 5}
	p := setupProvider(key)
	old := qqbot.Config{AIConnection: "site", SiteGroupID: 2, SiteAPIKeyID: 11, SiteOwnerID: 7, APIKey: "raw-secret"}
	conn, err := p.Prepare(context.Background(), nil, 7, 2, 20, old)
	require.NoError(t, err)
	require.Equal(t, "raw-secret", conn.Key)
	require.Equal(t, int64(11), conn.KeyID)
	require.False(t, conn.Created)
	require.Equal(t, float64(5), key.QuotaUsed)
	_, err = p.Prepare(context.Background(), nil, 7, 2, 21, old)
	require.Error(t, err)
	expired := time.Now().Add(-time.Hour)
	key.ExpiresAt = &expired
	_, err = p.Prepare(context.Background(), nil, 7, 2, 20, old)
	require.Error(t, err)
	key.ExpiresAt = nil
	key.Key = APIKeyStoredValue("rotated")
	_, err = p.Prepare(context.Background(), nil, 7, 2, 20, old)
	require.Error(t, err)
}
func TestQQBotManagedKeyCreatesInsideCallerTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{})
	require.NoError(t, err)
	mock.ExpectQuery("INSERT INTO api_keys").WithArgs(int64(7), sqlmock.AnyArg(), "QQ bot app", int64(2), StatusActive, float64(20)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(12))
	p := setupProvider(nil)
	conn, err := p.Prepare(context.Background(), tx, 7, 2, 20, qqbot.Config{AppID: "app"})
	require.NoError(t, err)
	require.NotEmpty(t, conn.Key)
	require.Equal(t, int64(12), conn.KeyID)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
