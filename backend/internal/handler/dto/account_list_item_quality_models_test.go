package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountListItemCarriesModelQualityStates(t *testing.T) {
	states := map[string]any{
		"model-a": map[string]any{"status": "blocked", "reason": "capability check failed"},
		"model-b": map[string]any{"status": "normal"},
		"model-c": map[string]any{"status": "pending"},
	}
	account := &service.Account{
		ID: 62, Status: service.StatusActive, Schedulable: true,
		Extra: map[string]any{
			"active_test_model_states": states,
			"active_test_model_holds":  map[string]any{"model-a": "capability check failed"},
		},
	}
	item := AccountListItemFromAccount(AccountFromServiceShallow(account))
	require.Equal(t, states, item.Extra["active_test_model_states"])
	require.Equal(t, account.Extra["active_test_model_holds"], item.Extra["active_test_model_holds"])
	require.Equal(t, service.StatusActive, item.Status)
	require.True(t, item.Schedulable)
}
