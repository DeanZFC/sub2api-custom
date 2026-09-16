package service

import "testing"

func TestAccountProtectionPolicyFromExtraDefaultsToDisabled(t *testing.T) {
	p := AccountProtectionPolicyFromExtra(nil)
	if p.Enabled || p.Mode != "observe" || p.AdaptiveMinConcurrency != 1 {
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestEffectiveAccountConcurrencyNeverRaisesAdminLimit(t *testing.T) {
	a := &Account{ID: 91001, Concurrency: 3, Extra: map[string]any{
		AccountProtectionPolicyKey: map[string]any{
			"enabled": true, "adaptive_concurrency": true, "adaptive_mode": "automatic",
			"adaptive_min_concurrency": 1,
		},
	}}
	if got := EffectiveAccountConcurrency(a, 3); got != 3 {
		t.Fatalf("first effective limit = %d, want 3", got)
	}
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	if got := EffectiveAccountConcurrency(a, 3); got != 1 {
		t.Fatalf("adaptive limit after failures = %d, want 1", got)
	}
}

func TestObserveAccountProtectionOutcomeDoesNotRegisterUnprotectedAccount(t *testing.T) {
	a := &Account{ID: 91002, Concurrency: 3, Extra: map[string]any{}}
	if got := EffectiveAccountConcurrency(a, 3); got != 3 {
		t.Fatal(got)
	}
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	if got := EffectiveAccountConcurrency(a, 3); got != 3 {
		t.Fatalf("unprotected limit changed to %d", got)
	}
}

func TestDisablingAccountProtectionRestoresConfiguredConcurrency(t *testing.T) {
	a := &Account{ID: 91003, Concurrency: 3, Extra: map[string]any{
		AccountProtectionPolicyKey: map[string]any{
			"enabled": true, "adaptive_concurrency": true, "adaptive_mode": "automatic",
			"adaptive_min_concurrency": 1,
		},
	}}
	_ = EffectiveAccountConcurrency(a, 3)
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	ObserveAccountProtectionOutcome(a.ID, 429, false)
	if got := EffectiveAccountConcurrency(a, 3); got != 1 {
		t.Fatalf("adaptive limit after failures = %d, want 1", got)
	}
	a.Extra[AccountProtectionPolicyKey].(map[string]any)["enabled"] = false
	if got := EffectiveAccountConcurrency(a, 3); got != 3 {
		t.Fatalf("disabled protection retained runtime limit %d, want configured 3", got)
	}
}
