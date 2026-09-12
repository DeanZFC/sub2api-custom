package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfiguredTestModelIDsUsesMappingKeys(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.4":  "gpt-5.4",
				"gpt-5.6":  "gpt-5.6",
				"*-hidden": "skip",
			},
		},
	}
	require.Equal(t, []string{"gpt-5.4", "gpt-5.6"}, ConfiguredTestModelIDs(account))
}

func TestConfiguredTestModelIDsIgnoresPassthrough(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-5": "gpt-5.1"},
		},
		Extra: map[string]any{"openai_passthrough": true},
	}
	require.Empty(t, ConfiguredTestModelIDs(account))
}
