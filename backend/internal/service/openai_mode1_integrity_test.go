package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func mode1TestAccount() *Account {
	return &Account{ID: 8201, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		AntiDegradeMarkerExtraKey: map[string]any{"enabled": true, "mode": string(AntiDegradeMode1), "policy_version": 3, "max_concurrency": 3},
	}}
}

func TestMode1RequestIntegrityAllowsUnchangedPayload(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"hello"}],"tools":[]}`)
	require.NoError(t, validateMode1RequestIntegrityForAccount(mode1TestAccount(), body, body))
}

func TestMode1RequestIntegrityRejectsDroppedSemanticField(t *testing.T) {
	before := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"hello"}],"tools":[{"type":"function","name":"lookup"}]}`)
	after := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"hello"}],"tools":[]}`)
	err := validateMode1RequestIntegrityForAccount(mode1TestAccount(), before, after)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tools")
}

func TestMode1RequestIntegrityAllowsCodexUnsupportedTokenBudgetOmission(t *testing.T) {
	before := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"hello"}],"max_output_tokens":128}`)
	after := []byte(`{"model":"gpt-5-codex","input":[{"role":"user","content":"hello"}]}`)
	require.NoError(t, validateMode1RequestIntegrityForAccount(mode1TestAccount(), before, after))
}

func TestRequestIntegrityDisabledForUnprotectedAccount(t *testing.T) {
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
	require.Equal(t, "off", a.RequestIntegrityMode())
}
