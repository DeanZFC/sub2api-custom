package repository

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSharedProxyCredentialsAreEncryptedAndRoundTrip(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	ciphertext, err := encryptSharedProxyCredentials(key, "user", "secret")
	require.NoError(t, err)
	require.NotContains(t, ciphertext, "user")
	require.NotContains(t, ciphertext, "secret")
	username, password := decryptSharedProxyCredentials(ciphertext, key)
	require.Equal(t, "user", username)
	require.Equal(t, "secret", password)
	_, wrongPassword := decryptSharedProxyCredentials(ciphertext, []byte("abcdefghijklmnopqrstuvwxyz123456"))
	require.Empty(t, wrongPassword)
}
