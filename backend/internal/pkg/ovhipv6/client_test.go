package ovhipv6

import "testing"

func TestAddressUsesEntirePrefixAndNeverWraps(t *testing.T) {
	tests := map[string]string{"1": "2001:db8:205:b900::1", "1000": "2001:db8:205:b900::3e8", "18446744073709551616": "2001:db8:205:b901::", "4722366482869645213695": "2001:db8:205:b9ff:ffff:ffff:ffff:ffff"}
	for n, want := range tests {
		got, err := Address("2001:db8:205:b900::/56", n)
		if err != nil || got.String() != want {
			t.Fatalf("%s: %s %v", n, got, err)
		}
	}
	for _, n := range []string{"0", "-1", "not-a-number", "4722366482869645213696"} {
		if _, err := Address("2001:db8:205:b900::/56", n); err == nil {
			t.Fatalf("accepted %s", n)
		}
	}
	if _, err := Address("2001:db8:205:b901::/56", "1"); err == nil {
		t.Fatal("accepted noncanonical prefix")
	}
	if got, err := Next("18446744073709551615"); err != nil || got != "18446744073709551616" {
		t.Fatalf("counter wrapped: %s %v", got, err)
	}
}
