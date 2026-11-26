package repository

import (
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Shared by the Ops dashboard and passive channel monitor. The denominator
// includes first-token wait for streaming requests.
const usageOutputTPSSQL = "ul.output_tokens * 1000.0 / NULLIF(ul.duration_ms, 0)"

var usageOutputTPSEligibleSQL = fmt.Sprintf(`ul.output_tokens > 0 AND ul.duration_ms > 0
 AND ul.image_count = 0 AND ul.image_output_tokens = 0
 AND COALESCE(ul.billing_mode, '') <> 'image'
 AND ul.request_type IN (%d, %d, %d, %d)`,
	service.RequestTypeSync, service.RequestTypeStream, service.RequestTypeWSV2, service.RequestTypeCyberBlocked)
