package service

import "encoding/json"

// Prism has no captured native verbosity control. Accept the standard text
// options, preserving non-default verbosity as a best-effort style instruction.
// Structured output still requires upstream support and must not be discarded.
func parsePrismTextOptions(raw json.RawMessage, field string) (string, error) {
	var options map[string]json.RawMessage
	if json.Unmarshal(raw, &options) != nil {
		return "", prismUnsupported(field)
	}
	var verbosity string
	formatField := field
	if field == "text" {
		for key := range options {
			if key != "format" && key != "verbosity" {
				return "", prismUnsupported(field)
			}
		}
		if value, ok := options["verbosity"]; ok && string(value) != "null" {
			if json.Unmarshal(value, &verbosity) != nil {
				return "", prismUnsupported("text.verbosity")
			}
			switch verbosity {
			case "low", "medium", "high":
			default:
				return "", &prismRequestError{"text.verbosity", "Prism text verbosity must be low, medium, or high"}
			}
		}
		format := options["format"]
		options = nil
		formatField = "text.format"
		if len(format) > 0 && json.Unmarshal(format, &options) != nil {
			return "", prismUnsupported(formatField)
		}
	}
	// Check type first so a json_schema object reports its unsupported format,
	// rather than whichever schema property happens to appear first in the map.
	if rawType, ok := options["type"]; ok {
		var formatType string
		if json.Unmarshal(rawType, &formatType) != nil || formatType != "text" {
			return "", &prismRequestError{formatField + ".type", "Prism supports plain text output only; set the output format type to text or omit the format"}
		}
	}
	for key := range options {
		if key != "type" {
			return "", prismUnsupported(formatField)
		}
	}
	return verbosity, nil
}

func prismVerbosityInstruction(verbosity string) string {
	switch verbosity {
	case "low":
		return "Response style preference: keep the final answer concise, while including what is needed to fulfill the request. Follow the caller's explicit instructions for content, language, and format."
	case "high":
		return "Response style preference: provide a detailed final answer, with useful explanations and examples when appropriate. Follow the caller's explicit instructions for content, language, and format."
	default:
		// An omitted or medium preference retains the original conversation.
		return ""
	}
}
