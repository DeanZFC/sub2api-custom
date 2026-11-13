package repository

import (
	"context"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
)

const ovhIPv6StorageQuery = `SELECT COUNT(*),
    COUNT(*) FILTER(WHERE state='reserved'),
    COUNT(*) FILTER(WHERE state='bound'),
    COUNT(*) FILTER(WHERE state='retiring'),
    pg_table_size('ovh_ipv6_leases'::regclass),
    pg_indexes_size('ovh_ipv6_leases'::regclass),
    pg_total_relation_size('ovh_ipv6_leases'::regclass)
    FROM ovh_ipv6_leases`

func (r *proxyRepository) GetOVHIPv6Storage(ctx context.Context) (*ovhipv6.StorageStats, error) {
	out := &ovhipv6.StorageStats{
		SampledAt: time.Now().UTC(),
		Files:     ovhipv6.ReadStorageFiles(os.Getenv("OVH_IPV6_STATE_STATS_DIR")),
	}
	// Neither a failed control request nor a missing mount may turn an unknown
	// count into zero. Each source remains useful if another is unavailable.
	controlCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var status ovhipv6.Status
	err := r.ipv6.Call(controlCtx, http.MethodGet, "/v1/status", nil, &status)
	cancel()
	if err != nil {
		out.ControlError = "无法读取 IPv6 出口服务的已发放序号"
	} else if n, ok := new(big.Int).SetString(status.Issued, 10); !ok || n.Sign() < 0 || n.String() != status.Issued {
		out.ControlError = "IPv6 出口服务返回的序号无效"
	} else {
		out.Issued, out.Prefix = &status.Issued, status.Prefix
		out.ControlAvailable = status.Available
		if !status.Available {
			out.ControlError = "IPv6 出口服务当前不可用"
		}
	}
	if !r.ipv6Supported || r.sql == nil {
		out.Database.Error = "IPv6 租约表统计不可用"
		return out, nil
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var rows, reserved, bound, retiring, tableBytes, indexBytes, totalBytes int64
	err = scanSingleRow(dbCtx, r.sql, ovhIPv6StorageQuery, nil, &rows, &reserved, &bound, &retiring, &tableBytes, &indexBytes, &totalBytes)
	if err != nil {
		out.Database.Error = "无法读取 IPv6 租约表的实际占用"
		return out, nil
	}
	out.Database = ovhipv6.StorageDatabase{
		Available: true, Rows: &rows, Reserved: &reserved, Bound: &bound, Retiring: &retiring,
		TableBytes: &tableBytes, IndexBytes: &indexBytes, TotalBytes: &totalBytes,
	}
	return out, nil
}
