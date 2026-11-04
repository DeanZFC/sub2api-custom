package httpclient

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"
)

func TestEvictProxyClientsDropsOnlyThatProxy(t *testing.T) {
	ephemeral := "http://probe:one-time@127.0.0.1:1"
	kept := "http://127.0.0.1:2"
	first, err := GetClient(Options{ProxyURL: ephemeral, Timeout: time.Second})
	require.NoError(t, err)
	_, err = GetClient(Options{ProxyURL: ephemeral, Timeout: 2 * time.Second, ResponseHeaderTimeout: time.Second})
	require.NoError(t, err)
	other, err := GetClient(Options{ProxyURL: kept, Timeout: time.Second})
	require.NoError(t, err)
	direct, err := GetClient(Options{Timeout: time.Second})
	require.NoError(t, err)

	EvictProxyClients(" " + ephemeral + " ")
	EvictProxyClients("  ")

	cached := 0
	sharedClients.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, ephemeral+"|") {
			cached++
		}
		return true
	})
	require.Zero(t, cached, "every client built for the proxy is evicted")
	again, err := GetClient(Options{ProxyURL: ephemeral, Timeout: time.Second})
	require.NoError(t, err)
	require.NotSame(t, first, again)
	same, err := GetClient(Options{ProxyURL: kept, Timeout: time.Second})
	require.NoError(t, err)
	require.Same(t, other, same, "other proxies keep their pooled client")
	sameDirect, err := GetClient(Options{Timeout: time.Second})
	require.NoError(t, err)
	require.Same(t, direct, sameDirect, "an empty URL never evicts direct clients")
	EvictProxyClients(ephemeral)
}
