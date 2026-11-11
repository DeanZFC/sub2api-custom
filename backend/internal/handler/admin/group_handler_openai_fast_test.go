package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRequestsDecodeForceOpenAIFast(t *testing.T) {
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"name":"fast","force_openai_fast":true,"free_openai_fast":true}`), &createReq))
	require.True(t, createReq.ForceOpenAIFast)
	require.True(t, createReq.FreeOpenAIFast)

	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"force_openai_fast":false,"free_openai_fast":false}`), &updateReq))
	require.NotNil(t, updateReq.ForceOpenAIFast)
	require.False(t, *updateReq.ForceOpenAIFast)
	require.NotNil(t, updateReq.FreeOpenAIFast)
	require.False(t, *updateReq.FreeOpenAIFast)

	var omitted UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omitted))
	require.Nil(t, omitted.ForceOpenAIFast)
	require.Nil(t, omitted.FreeOpenAIFast)
}

func TestGroupRequestsDecodeForcedServiceTier(t *testing.T) {
	var createReq CreateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"force_openai_service_tier":"ultrafast"}`), &createReq))
	require.NotNil(t, createReq.ForceOpenAIServiceTier)
	require.Equal(t, "ultrafast", *createReq.ForceOpenAIServiceTier)

	var updateReq UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"force_openai_service_tier":""}`), &updateReq))
	require.NotNil(t, updateReq.ForceOpenAIServiceTier)
	require.Empty(t, *updateReq.ForceOpenAIServiceTier)

	var omittedCreate CreateGroupRequest
	var omittedUpdate UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omittedCreate))
	require.NoError(t, json.Unmarshal([]byte(`{}`), &omittedUpdate))
	require.Nil(t, omittedCreate.ForceOpenAIServiceTier)
	require.Nil(t, omittedUpdate.ForceOpenAIServiceTier)
}
