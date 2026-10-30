package requestmodel

import (
	"errors"
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
)

// ErrDuplicateModelFields identifies a model selector that different JSON
// parsers can interpret differently. Even repeated identical values are rejected.
var ErrDuplicateModelFields = errors.New("duplicate or case-conflicting model fields are not allowed")

// ErrDuplicateSessionFields prevents a WebSocket session model selector from
// being replaced by a later, differently parsed session object.
var ErrDuplicateSessionFields = errors.New("duplicate or case-conflicting session fields are not allowed")

// ValidateJSONModelFields rejects multiple top-level keys that decode to model
// (case-insensitively), regardless of their values or JSON escape spelling. It
// must run before routing, model rewriting, or decoding into a map/struct, which
// would discard duplicate keys. Nested tool schemas and user input are untouched.
//
// Missing model fields, malformed JSON, and non-object payloads remain the
// responsibility of the endpoint's existing request validation.
func ValidateJSONModelFields(body []byte) error {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	// This view is synchronous and read-only, and no body-backed string escapes
	// the function. Avoid copying potentially large inline image/audio payloads.
	view := gjson.Parse(unsafe.String(unsafe.SliceData(body), len(body)))
	if !view.IsObject() {
		return nil
	}
	return validateObjectModelFields(view)
}

// ValidateWebSocketModelFields also validates session.model, which can change
// the effective model for later turns. Only protocol selector locations are
// inspected; model fields inside input, tools, and metadata are not selectors.
func ValidateWebSocketModelFields(body []byte) error {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil
	}
	view := gjson.Parse(unsafe.String(unsafe.SliceData(body), len(body)))
	if !view.IsObject() {
		return nil
	}
	if err := validateObjectModelFields(view); err != nil {
		return err
	}
	seenSession := false
	var err error
	view.ForEach(func(key, value gjson.Result) bool {
		if !strings.EqualFold(key.String(), "session") {
			return true
		}
		if seenSession {
			err = ErrDuplicateSessionFields
			return false
		}
		seenSession = true
		if value.IsObject() {
			err = validateObjectModelFields(value)
		}
		return err == nil
	})
	return err
}

func validateObjectModelFields(view gjson.Result) error {
	seen := false
	var err error
	view.ForEach(func(key, _ gjson.Result) bool {
		if !strings.EqualFold(key.String(), "model") {
			return true
		}
		if seen {
			err = ErrDuplicateModelFields
			return false
		}
		seen = true
		return true
	})
	return err
}
