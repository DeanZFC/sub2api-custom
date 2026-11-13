package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
)

type OVHIPv6StorageAdmin interface {
	GetOVHIPv6Storage(context.Context) (*ovhipv6.StorageStats, error)
}

func (s *adminServiceImpl) GetOVHIPv6Storage(ctx context.Context) (*ovhipv6.StorageStats, error) {
	repo, ok := s.proxyRepo.(OVHIPv6StorageAdmin)
	if !ok {
		return nil, fmt.Errorf("OVH IPv6 存储统计不可用")
	}
	return repo.GetOVHIPv6Storage(ctx)
}
