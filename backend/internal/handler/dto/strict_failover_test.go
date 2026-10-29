package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStrictFailoverAdminOnlyDTO(t *testing.T) {
	g := &service.Group{ID: 1, StrictFailover: service.StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{11, 22}, {33}}}}
	admin, err := json.Marshal(GroupFromServiceAdmin(g))
	require.NoError(t, err)
	require.Contains(t, string(admin), "\"strict_failover\":{\"enabled\":true,\"tiers\":[[11,22],[33]]}")
	public, err := json.Marshal(GroupFromService(g))
	require.NoError(t, err)
	require.NotContains(t, string(public), "strict_failover")
}
