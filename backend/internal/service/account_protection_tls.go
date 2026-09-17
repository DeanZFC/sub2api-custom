package service

import (
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// resolveAntiDegradeTLSProfile resolves built-in strategy profiles. It returns
// nil for the standard transport and for unprotected accounts, preserving the
// original HTTP transport exactly when the account switch is off.
func resolveAntiDegradeTLSProfile(a *Account) (*tlsfingerprint.Profile, error) {
	if a == nil || !a.IdentityProtectionEnabled() {
		return nil, nil
	}
	mode := antiDegradeMode(a)
	p := antiDegradeStrategyProfile(mode)
	if p.ID == "" {
		return nil, fmt.Errorf("unknown protection strategy: %s", mode)
	}
	name := strings.TrimSpace(antiDegradeAccountTLSProfile(a))
	if name == "" || name == "standard" || name == "account" || name == "mac_codex" {
		return nil, nil
	}
	profile := tlsfingerprint.BuiltinProfile(name)
	if profile == nil {
		return nil, fmt.Errorf("TLS profile unavailable: %s", name)
	}
	return profile, nil
}

// resolveProtectionTransport applies the deployment kill switch only to the
// explicit protection strategy. Unprotected accounts retain their old path.
func resolveProtectionTransport(a *Account, cfg *config.Config) (*tlsfingerprint.Profile, error) {
	if err := validateRegisteredAntiDegrade(a); err != nil {
		return nil, err
	}
	p, err := resolveAntiDegradeTLSProfile(a)
	if err != nil {
		return nil, err
	}
	if a != nil && a.IdentityProtectionEnabled() && cfg != nil && !cfg.Gateway.TLSFingerprint.Enabled {
		return nil, nil
	}
	if a != nil && a.IdentityProtectionEnabled() && antiDegradeMode(a) == AntiDegradeModeSingleMachine {
		return tlsfingerprint.NewMacCodexProfile(), nil
	}
	return p, nil
}

func resolveAccountTLSProfileForOpenAI(a *Account, cfg *config.Config) (*tlsfingerprint.Profile, error) {
	if a != nil && a.IdentityProtectionEnabled() {
		return resolveProtectionTransport(a, cfg)
	}
	return resolveCodexMacTLSProfile(a), nil
}
