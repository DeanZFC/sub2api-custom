package service

import (
	"strings"

	"github.com/tidwall/gjson"
)

// A transport terminator alone does not prove successful generation. Preserve
// truncated, filtered and unknown completion states for request accounting.
func openAICCFinishState(payload []byte) (finished, incomplete bool) {
	view := gjson.ParseBytes(payload)
	if e := view.Get("error"); e.Exists() && e.Type != gjson.Null {
		incomplete = true
	}
	for _, choice := range view.Get("choices").Array() {
		reason := strings.TrimSpace(choice.Get("finish_reason").String())
		switch reason {
		case "":
		case "stop", "tool_calls", "function_call":
			finished = true
		default:
			incomplete = true
		}
	}
	return
}
