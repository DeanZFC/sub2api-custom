package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPassiveCacheAnthropicRequiresKnownFinalCacheBoundary(t *testing.T) {
	for _, content := range []any{
		"",
		[]any{map[string]any{"type": "text", "text": ""}},
		[]any{map[string]any{"type": "thinking", "thinking": "hidden"}},
		[]any{map[string]any{"type": "tool_result", "content": "result"}},
	} {
		root := map[string]any{"cache_control": map[string]any{"type": "ephemeral"}, "messages": []any{map[string]any{"role": "user", "content": content}}}
		require.False(t, passiveQualityAnthropicFullPromptCache(root))
	}
}

func TestPassiveCachePrefixPreservesToolInputNamedCacheControl(t *testing.T) {
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{
		"type": "tool_use", "input": map[string]any{"cache_control": "user content"}, "cache_control": map[string]any{"type": "ephemeral"},
	}}}
	cleanMessage, ok := passiveQualityWithoutCacheMarkers(message).(map[string]any)
	require.True(t, ok)
	content, ok := cleanMessage["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	clean, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, clean, "cache_control")
	input, ok := clean["input"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "user content", input["cache_control"])
}

func TestPassiveCacheAnthropicBoundsPrefixLookupByContentBlocks(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}}
	first := map[string]any{"model": "claude-sonnet-4-5", "cache_control": map[string]any{"type": "ephemeral"}, "messages": []any{map[string]any{"role": "user", "content": "initial"}}}
	body, err := json.Marshal(first)
	require.NoError(t, err)
	prior := buildPassiveQualityCacheMetadata(a, body, "s", "claude-sonnet-4-5", 1, time.Now())
	require.Empty(t, prior.UnsupportedReason)
	for _, count := range []int{19, 20} {
		blocks := make([]any, count)
		for i := range blocks {
			blocks[i] = map[string]any{"type": "text", "text": "more"}
		}
		first["messages"] = []any{map[string]any{"role": "user", "content": "initial"}, map[string]any{"role": "user", "content": blocks}}
		body, err = json.Marshal(first)
		require.NoError(t, err)
		current := buildPassiveQualityCacheMetadata(a, body, "s", "claude-sonnet-4-5", 1, time.Now())
		require.Empty(t, current.UnsupportedReason)
		if count < 20 {
			require.Contains(t, current.PrefixHashes, prior.PromptHash)
		} else {
			require.NotContains(t, current.PrefixHashes, prior.PromptHash)
		}
	}
}
