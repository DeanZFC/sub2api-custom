package ipv6egress

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
)

type storedLease struct {
	ovhipv6.Lease
	CreatedAt  time.Time `json:"created_at"`
	Configured bool      `json:"configured"`
}

type diskState struct {
	Version int                     `json:"version"`
	Prefix  string                  `json:"prefix"`
	Leases  map[string]*storedLease `json:"leases"`
}

type counterState struct {
	Version int    `json:"version"`
	Prefix  string `json:"prefix"`
	Issued  string `json:"issued"`
}

type store struct {
	dir, prefix string
	unlock      func() error
}

func openStore(dir, prefix string, initialize bool) (*store, string, diskState, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", diskState{}, err
	}
	unlock, err := lockState(filepath.Join(dir, ".lock"))
	if err != nil {
		return nil, "", diskState{}, err
	}
	s := &store{dir: dir, prefix: prefix, unlock: unlock}
	fail := func(err error) (*store, string, diskState, error) { _ = unlock(); return nil, "", diskState{}, err }
	names := []string{"counter.json", "counter.guard.json", "leases.json"}
	if initialize {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
				return fail(errors.New("initialization refused: state already exists; never reset allocation counters"))
			}
		}
		if err := s.saveCounter("0"); err != nil {
			return fail(err)
		}
		if err := s.saveLeases(map[string]*storedLease{}); err != nil {
			return fail(err)
		}
	}
	issued := new(big.Int)
	for _, name := range names[:2] {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fail(fmt.Errorf("allocation counter unavailable; refusing to initialize automatically: %w", err))
		}
		var c counterState
		if err := json.Unmarshal(b, &c); err != nil {
			return fail(err)
		}
		n, ok := new(big.Int).SetString(c.Issued, 10)
		if c.Version != 1 || c.Prefix != prefix || !ok || n.Sign() < 0 || n.String() != c.Issued {
			return fail(errors.New("invalid or mismatched allocation counter"))
		}
		if n.Sign() > 0 {
			if _, err := ovhipv6.Address(prefix, c.Issued); err != nil {
				return fail(err)
			}
		}
		if n.Cmp(issued) > 0 {
			issued = n
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "leases.json"))
	if err != nil {
		return fail(fmt.Errorf("lease state missing; refusing to reconstruct from a database: %w", err))
	}
	var d diskState
	if err = json.Unmarshal(b, &d); err != nil {
		return fail(err)
	}
	if d.Version != 1 || d.Prefix != prefix || d.Leases == nil {
		return fail(errors.New("invalid lease state"))
	}
	for id, l := range d.Leases {
		n, ok := new(big.Int).SetString(id, 10)
		if !ok || n.Sign() <= 0 || n.String() != id || l == nil || l.ID != id {
			return fail(errors.New("invalid lease identifier"))
		}
		a, err := ovhipv6.Address(prefix, id)
		if err != nil || a.String() != l.Address || l.Username != "ovh6_"+id || len(l.Password) < 32 {
			return fail(errors.New("invalid lease identity"))
		}
		if l.State != "provisioning" && l.State != "pending" && l.State != "active" && l.State != "retiring" {
			return fail(errors.New("invalid lease state"))
		}
		if n.Cmp(issued) > 0 {
			issued = n
		}
	}
	// Taking the maximum protects against restoring only one counter file.
	if err = s.saveCounter(issued.String()); err != nil {
		return fail(err)
	}
	return s, issued.String(), d, nil
}

func (s *store) saveCounter(issued string) error {
	b, err := json.Marshal(counterState{Version: 1, Prefix: s.prefix, Issued: issued})
	if err != nil {
		return err
	}
	if err = atomicFile(filepath.Join(s.dir, "counter.json"), b); err != nil {
		return err
	}
	return atomicFile(filepath.Join(s.dir, "counter.guard.json"), b)
}

func (s *store) saveLeases(leases map[string]*storedLease) error {
	b, err := json.Marshal(diskState{Version: 1, Prefix: s.prefix, Leases: leases})
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(s.dir, "leases.json"), b)
}

func atomicFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".ipv6-state-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
