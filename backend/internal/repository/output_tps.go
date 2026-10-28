package repository

import "strings"

// The alias is an internal SQL identifier. Image inputs may produce text;
// generated media, compact requests and synthetic probes are not text speed.
func outputTPSEligibleSQL(alias string) string {
	return strings.ReplaceAll(`(COALESCE(u.stream,false) OR COALESCE(u.openai_ws_mode,false))
 AND COALESCE(u.request_type,0) NOT IN (4,6)
 AND u.output_tokens>0 AND u.first_token_ms>=0 AND u.duration_ms>u.first_token_ms
 AND COALESCE(u.image_count,0)=0 AND COALESCE(u.video_count,0)=0 AND COALESCE(u.image_output_tokens,0)=0
 AND COALESCE(u.inbound_endpoint,'') !~* '(^|/)(images|videos|audio|compact)(/|$)'
 AND COALESCE(u.upstream_endpoint,'') !~* '(^|/)(images|videos|audio|compact)(/|$)'
 AND NOT COALESCE(u.native_compaction_v2,false)`, "u.", alias+".")
}
