package service

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const scheduledTestNumberPattern = `[-+]?(?:(?:\d+(?:\.\d*)?)|(?:\.\d+))(?:[eE][-+]?\d+)?`
const scheduledTestMarkerGap = "[ \\t*_`~]*"
const scheduledTestNumberAfterMarker = scheduledTestMarkerGap + `(?:(?:is\b|are\b|为|是)` + scheduledTestMarkerGap + `)?(?:=|:|：)?` + scheduledTestMarkerGap + `(` + scheduledTestNumberPattern + `)`

var (
	scheduledTestNumberRE           = regexp.MustCompile(`(` + scheduledTestNumberPattern + `)`)
	scheduledTestStandaloneNumberRE = regexp.MustCompile(`^` + scheduledTestNumberPattern + `$`)
	scheduledTestFinalNumberRE      = regexp.MustCompile(`(?i)(?:\bfinal[ \t]+(?:answer|result)\b|最终[ \t]*(?:答案|结果)|最后[ \t]*(?:答案|结果))` + scheduledTestNumberAfterMarker)
	scheduledTestStrongNumberRE     = regexp.MustCompile(`(?i)(?:\b(?:answer|result)\b|答案|结果|结论)` + scheduledTestNumberAfterMarker)
	scheduledTestMinimumNumberRE    = regexp.MustCompile(`(?i)(?:\bminimum(?:[ \t]+number)?\b|最少[ \t]*(?:(?:需要|需|要)[ \t]*)?(?:取出|取|拿出|拿|抽取)?)` + scheduledTestNumberAfterMarker)
	scheduledTestAtLeastNumberRE    = regexp.MustCompile(`至少[ \t]*(?:(?:需要|需|要)[ \t]*)?(?:取出|取)?` + scheduledTestNumberAfterMarker)

	// Only unwrap scalar numbers. An expression such as \boxed{9+12} or
	// \(21/2\) must never be reduced to its first number.
	scheduledTestBoxedNumberRE = regexp.MustCompile(`\\(?:boxed|fbox)[ \t]*\{[ \t]*(` + scheduledTestNumberPattern + `)[ \t]*\}`)
	scheduledTestMathNumberREs = []*regexp.Regexp{
		regexp.MustCompile(`\\\([ \t]*(` + scheduledTestNumberPattern + `)[ \t]*\\\)`),
		regexp.MustCompile(`\\\[[ \t\r\n]*(` + scheduledTestNumberPattern + `)[ \t\r\n]*\\\]`),
		regexp.MustCompile(`\$\$[ \t\r\n]*(` + scheduledTestNumberPattern + `)[ \t\r\n]*\$\$`),
		regexp.MustCompile(`\$[ \t]*(` + scheduledTestNumberPattern + `)[ \t]*\$`),
	}
	scheduledTestFencedNumberRE = regexp.MustCompile("(?i)^```(?:text|number)?[ \\t]*\\r?\\n[ \\t]*(" + scheduledTestNumberPattern + ")[ \\t]*\\r?\\n```$")
)

func extractScheduledTestNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	if match := scheduledTestFencedNumberRE.FindStringSubmatch(trimmed); match != nil {
		return parseScheduledTestNumber(match[1])
	}
	formatted := scheduledTestBoxedNumberRE.ReplaceAllString(trimmed, "$1")
	for _, re := range scheduledTestMathNumberREs {
		formatted = re.ReplaceAllString(formatted, "$1")
	}
	bare := strings.TrimSpace(strings.Trim(formatted, "*_`~"))
	if scheduledTestStandaloneNumberRE.MatchString(bare) {
		return parseScheduledTestNumber(bare)
	}

	// A final answer outranks a stated minimum, which in turn outranks an
	// intermediate result or an "at least one" observation. Markers must be
	// immediately followed by a scalar, allowing formatting but no prose.
	for _, re := range []*regexp.Regexp{scheduledTestFinalNumberRE, scheduledTestMinimumNumberRE, scheduledTestStrongNumberRE} {
		if value, ok := scheduledTestNumberAfterMatch(formatted, re); ok {
			return value, true
		}
	}

	// A boxed scalar on the final line is also an explicit conclusion. Require
	// it to be the line's only number; never guess from an unmarked proof line.
	lines := strings.Split(trimmed, "\n")
	last := lines[len(lines)-1]
	if matches := scheduledTestBoxedNumberRE.FindAllStringSubmatch(last, -1); len(matches) == 1 && len(scheduledTestNumberRE.FindAllString(last, -1)) == 1 {
		formattedLines := strings.Split(formatted, "\n")
		return scheduledTestNumberAfterMatch(formattedLines[len(formattedLines)-1], scheduledTestNumberRE)
	}
	return scheduledTestNumberAfterMatch(formatted, scheduledTestAtLeastNumberRE)
}

func scheduledTestNumberAfterMatch(text string, re *regexp.Regexp) (float64, bool) {
	matches := re.FindAllStringSubmatchIndex(text, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][2], matches[i][3]
		// Reject partial numbers and arithmetic rather than read "21/2" as 21,
		// "1,000" as 1, or an overflowing exponent as a usable finite value.
		suffix := strings.TrimLeft(text[end:], "*_`~")
		if suffix != "" && (suffix[0] >= 'a' && suffix[0] <= 'z' || suffix[0] >= 'A' && suffix[0] <= 'Z') {
			continue
		}
		tail := strings.TrimLeft(suffix, " \t")
		if tail != "" {
			c, _ := utf8.DecodeRuneInString(tail)
			if strings.ContainsRune("0123456789+-*/^=<>\\×÷−±≤≥≠≈", c) {
				continue
			}
			if (c == ',' || c == '.') && len(tail) > 1 && (tail[1] >= '0' && tail[1] <= '9' || tail[1] == '.') {
				continue
			}
		}
		if n, ok := parseScheduledTestNumber(text[start:end]); ok {
			return n, true
		}
	}
	return 0, false
}

func parseScheduledTestNumber(text string) (float64, bool) {
	n, err := strconv.ParseFloat(text, 64)
	return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}
