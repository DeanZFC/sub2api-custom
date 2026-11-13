package ipv6egress

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestLinuxLeaseLifetimesPreserveDefaultSourceAndDAD(t *testing.T) {
	const lease = "2001:db8:205:b900::20"
	for _, restore := range []bool{false, true} {
		name := "create"
		if restore {
			name = "restore"
		}
		t.Run(name, func(t *testing.T) {
			lookups, mutations := 0, 0
			intentSaved := false
			a := LinuxAddresses{Interface: "eth0", BeforeAdd: func(string) error { intentSaved = true; return nil }}
			a.command = func(_ context.Context, args ...string) ([]byte, error) {
				if slices.Equal(args, []string{"-j", "-6", "address", "show", "dev", "eth0"}) {
					lookups++
					addresses := []addressInfo{{Local: "2001:db8:205:b900::1", PrefixLen: 56}}
					if restore || mutations > 0 {
						// A deprecated address may still be tentative. Do not use it
						// until the existing DAD wait sees that flag clear.
						addresses = append(addresses, addressInfo{Local: lease, PrefixLen: 128, Tentative: lookups == 2, Flags: []string{"deprecated"}})
					}
					return json.Marshal([]map[string]any{{"addr_info": addresses}})
				}
				op := "add"
				if restore {
					op = "change"
				}
				want := []string{"-6", "address", op, lease + "/128", "dev", "eth0", "preferred_lft", "0", "valid_lft", "forever"}
				if !slices.Equal(args, want) {
					t.Fatalf("unexpected network mutation: %q", args)
				}
				if !restore && !intentSaved {
					t.Fatal("kernel mutation preceded durable ownership")
				}
				mutations++
				return nil, nil
			}
			owned, err := a.Ensure(context.Background(), lease, restore)
			if err != nil || !owned || mutations != 1 || lookups != 3 {
				t.Fatalf("lease did not wait for DAD: owned=%v mutations=%d lookups=%d err=%v", owned, mutations, lookups, err)
			}
			if restore && intentSaved {
				t.Fatal("restoration unexpectedly claimed a new address")
			}
		})
	}
}

func TestLinuxLeaseNeverChangesPrimaryOrUnownedAddress(t *testing.T) {
	const address = "2001:db8:205:b900::1"
	for _, tc := range []struct {
		name    string
		prefix  int
		restore bool
	}{{"primary", 56, true}, {"unowned128", 128, false}} {
		t.Run(tc.name, func(t *testing.T) {
			a := LinuxAddresses{Interface: "eth0", command: func(_ context.Context, args ...string) ([]byte, error) {
				if len(args) == 0 || args[0] != "-j" {
					t.Fatalf("unowned address was mutated: %q", args)
				}
				return json.Marshal([]map[string]any{{"addr_info": []addressInfo{{Local: address, PrefixLen: tc.prefix}}}})
			}}
			if owned, err := a.Ensure(context.Background(), address, tc.restore); err == nil || owned {
				t.Fatalf("unowned address accepted: %v %v", owned, err)
			}
		})
	}
}

func TestLinuxRestoreDeprecationFailureIsNotReady(t *testing.T) {
	const address = "2001:db8:205:b900::20"
	a := LinuxAddresses{Interface: "eth0", command: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "-j" {
			return json.Marshal([]map[string]any{{"addr_info": []addressInfo{{Local: address, PrefixLen: 128}}}})
		}
		return nil, errors.New("kernel rejected change")
	}}
	owned, err := a.Ensure(context.Background(), address, true)
	if !owned || err == nil || !strings.Contains(err.Error(), "cannot deprecate") {
		t.Fatalf("failed lifetime repair accepted: %v %v", owned, err)
	}
}
