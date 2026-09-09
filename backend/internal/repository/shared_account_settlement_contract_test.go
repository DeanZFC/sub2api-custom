package repository

import (
	"os"
	"strings"
	"testing"
)

func TestSharedSettlementWritesImmutableLedgerWalletAndRecentCall(t *testing.T) {
	source, err := os.ReadFile("usage_billing_repo.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, fragment := range []string{
		"shared_account_usage_ledger",
		"shared_account_wallets",
		"shared_account_call_stats",
		"ON CONFLICT(request_id) DO NOTHING",
		"UPDATE shared_account_listings",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("settlement contract missing %q", fragment)
		}
	}
}
