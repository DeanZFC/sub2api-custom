package vmbridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type stateStore struct {
	dir  string
	aead cipher.AEAD
}
type sealedState struct {
	Version    int    `json:"version"`
	AccountID  int64  `json:"account_id"`
	Ciphertext string `json:"ciphertext"`
}

func newStateStore(dir, secret string) (*stateStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("state directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create state directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, errors.New("cannot protect state directory")
	}
	key := sha256.Sum256([]byte("vm2api-bridge-state-v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &stateStore{dir: dir, aead: aead}, nil
}

func (s *stateStore) path(id int64) string {
	return filepath.Join(s.dir, fmt.Sprintf("account-%d.json", id))
}
func (s *stateStore) save(state persistentAccount) error {
	plain, err := json.Marshal(state)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, plain, []byte(strconv.FormatInt(state.AccountID, 10)))
	data, err := json.Marshal(sealedState{Version: 1, AccountID: state.AccountID, Ciphertext: base64.StdEncoding.EncodeToString(sealed)})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".account-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	defer func() { _ = f.Close() }()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path(state.AccountID)); err != nil {
		return err
	}
	return s.syncDir()
}

func (s *stateStore) syncDir() error {
	f, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func (s *stateStore) remove(id int64) error {
	if err := os.Remove(s.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.syncDir()
}
func (s *stateStore) load() ([]persistentAccount, error) {
	files, err := filepath.Glob(filepath.Join(s.dir, "account-*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]persistentAccount, 0, len(files))
	for _, file := range files {
		info, err := os.Lstat(file)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return nil, errors.New("invalid state file")
		}
		if err := os.Chmod(file, 0600); err != nil {
			return nil, errors.New("cannot protect state file")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.New("cannot read state file")
		}
		var envelope sealedState
		if json.Unmarshal(data, &envelope) != nil || envelope.Version != 1 || envelope.AccountID <= 0 || s.path(envelope.AccountID) != file {
			return nil, errors.New("invalid state envelope")
		}
		sealed, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
		if err != nil || len(sealed) < s.aead.NonceSize() {
			return nil, errors.New("invalid encrypted state")
		}
		plain, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], []byte(strconv.FormatInt(envelope.AccountID, 10)))
		if err != nil {
			return nil, errors.New("cannot decrypt state; verify bridge secret")
		}
		var state persistentAccount
		if json.Unmarshal(plain, &state) != nil || state.AccountID != envelope.AccountID {
			return nil, errors.New("invalid account state")
		}
		if state.State == stateReady && (state.Resources.VMID == "" || state.Resources.APIKey == "") {
			return nil, errors.New("incomplete ready account state")
		}
		out = append(out, state)
	}
	return out, nil
}
