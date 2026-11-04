package httpclient

import "strings"

// EvictProxyClients 移除为指定代理 URL 缓存的全部客户端
// 仅用于随进程消失的临时代理（如 Mihomo 单节点检测监听）：每次检测的端口与凭据都不同，
// 不移除会让缓存随检测次数无限增长。空 URL 直接忽略，不影响直连客户端。
func EvictProxyClients(proxyURL string) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return
	}
	prefix := proxyURL + "|"
	sharedClients.Range(func(key, _ any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			sharedClients.Delete(key)
		}
		return true
	})
}
