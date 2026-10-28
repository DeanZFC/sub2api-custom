package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupQualityFallbackAccountKeepsActiveModelHold(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		row := groupQualityAccountRow{ID: 1, Platform: "anthropic", Type: "apikey", Status: "active", Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"model-a": "upstream-a", "model-b": "upstream-b"}},
			Extra:       map[string]any{"active_test_model_holds": map[string]any{"model-a": "active ability test failed"}},
		}
		if fallback {
			row.Status = "quality_paused"
		}
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		allowed, err := groupQualityAccountEligible(context.Background(), raw, "model-a", fallback, true)
		require.NoError(t, err)
		require.False(t, allowed, "fallback must not relax an active model verdict")
		allowed, err = groupQualityAccountEligible(context.Background(), raw, "model-b", fallback, true)
		require.NoError(t, err)
		require.True(t, allowed, "another model is not blocked by model-a's hold")
	}
}

func TestGroupQualityFallbackAccountCannotBypassAllModelPause(t *testing.T) {
	row := groupQualityAccountRow{ID: 1, Platform: "anthropic", Type: "apikey", Status: "quality_paused", Schedulable: true,
		Extra: map[string]any{"active_test_model_holds": map[string]any{"model-a": "failed"}, "active_test_all_models_paused": true},
	}
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	for _, model := range []string{"model-a", "unmonitored-model", ""} {
		allowed, err := groupQualityAccountEligible(context.Background(), raw, model, true, true)
		require.NoError(t, err)
		require.False(t, allowed, "an all-model pause covers every requested model")
	}
}
