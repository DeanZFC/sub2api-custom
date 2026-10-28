package repository

import "strings"

// A billed partial response can still fail, and a free response can succeed.
// Use terminal request outcomes independently of the legacy billing metrics.
// WS error IDs identify connections, so they cannot invalidate successful turns.
const channelMonitorV2FinalFailureKeysSQL = `WITH tps_final_failure_keys AS MATERIALIZED (
 SELECT DISTINCT COALESCE(e.api_key_id,0) AS api_key_id,COALESCE(e.account_id,0) AS account_id,keys.request_id
 FROM ops_error_logs e CROSS JOIN LATERAL unnest(ARRAY[
  'client:'||NULLIF(TRIM(e.client_request_id),''), 'local:'||NULLIF(TRIM(e.request_id),''),
  NULLIF(TRIM(e.request_id),''), NULLIF(TRIM(e.client_request_id),'')
 ]) keys(request_id)
 WHERE e.created_at >= $1 AND e.created_at < $2
 AND NOT COALESCE(e.is_count_tokens,false) AND COALESCE(e.request_type,0)<>3
 AND (COALESCE(e.status_code,0)>=400 OR e.error_type='cyber_policy')
 AND keys.request_id IS NOT NULL
)
`

func channelMonitorV2NoFinalFailureSQL(alias string) string {
	return strings.ReplaceAll(`(COALESCE(u.request_type,0)=3 OR COALESCE(u.openai_ws_mode,false) OR NOT EXISTS (
 SELECT 1 FROM tps_final_failure_keys e
 WHERE e.api_key_id=COALESCE(u.api_key_id,0) AND e.account_id=COALESCE(u.account_id,0)
 AND e.request_id=NULLIF(TRIM(u.request_id),'')
))`, "u.", alias+".")
}
