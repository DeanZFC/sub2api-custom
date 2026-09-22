package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractScheduledTestHTMLSelectsFinalReportedAnimation(t *testing.T) {
	raw, err := os.ReadFile("testdata/scheduled_test_pelican_revised_transcript.txt")
	require.NoError(t, err)
	source := string(raw)
	commandStart := strings.Index(source, `{"cmd":"cat > pelican-bike.html`)
	require.NotEqual(t, -1, commandStart)
	var command struct {
		Cmd string `json:"cmd"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(source[commandStart:])).Decode(&command))
	start := strings.Index(command.Cmd, "<!doctype html>")
	end := strings.LastIndex(command.Cmd, "</html>") + len("</html>")
	want := command.Cmd[start:end]

	result := &ScheduledTestResult{Status: "success", OutputKind: "html", ResponseText: source}
	(&ScheduledTestRunnerService{}).applyOutputContract(result, "html")
	require.Equal(t, want, result.OutputHTML, "the initial text-only patch must not hide the later complete animation")
	require.Equal(t, "success", result.Status)
	require.Contains(t, result.OutputHTML, `class="pelican-bob"`)
	require.Contains(t, result.OutputHTML, `@keyframes wheel-roll`)
	require.Equal(t, source, result.ResponseText)

	result.OutputHTML = `<html><body><svg viewBox="0 0 100 100"><text x="10" y="50">鹈鹕骑自行车</text></svg></body></html>`
	normalizeStoredTestResults([]*ScheduledTestResult{result})
	require.Equal(t, want, result.OutputHTML, "existing history must be repaired from the original response on read")
	require.Equal(t, source, result.ResponseText)
}

func TestExtractScheduledTestHTMLSelectsLatestCompleteDocument(t *testing.T) {
	first := `<html><body><svg><text>First long placeholder</text></svg></body></html>`
	last := `<html><body><svg><circle r="20"/></svg></body></html>`
	withScript := `<html><body><svg><circle r="20"/></svg><script>const example = "</html><html><body>example</body></html>"; const template = "<svg></svg>"; if (1 < 2) document.body.dataset.ok = "yes";</script></body></html>`
	withRawText := `<html><head><title>Example <svg></svg></title><style>/* </html><html>example</html> */ .art::before { content: "<svg></svg>" }</style></head><body><textarea><html>example</html></textarea><svg><svg><circle r="20"/></svg></svg></body></html>`
	for _, tc := range []struct{ name, source, want string }{
		{"later smaller full document", first + "\nRevised:\n" + last, last},
		{"revision after closed code fence", "```html\n" + first + "\n```\nFinal:\n" + last, last},
		{"later svg", `<svg><text>placeholder</text></svg>` + "\nFinal:\n" + `<svg><circle r="20"/></svg>`, `<svg><circle r="20"/></svg>`},
		{"nested roots stay in final document", first + "\n" + last, last},
		{"trailing incomplete html", first + `\n<html><body><svg><circle r="20"/></svg>`, first},
		{"trailing incomplete svg", first + `\n<svg><circle r="20"/>`, first},
		{"trailing markup explanation", last + "\nUse `<circle>` or `<p>notes</p>` to edit it.", last},
		{"raw script markup", first + "\n" + withScript, withScript},
		{"raw style and textarea markup", first + "\n" + withRawText, withRawText},
		{"comment is not a candidate", `<!-- <html><body>example</body></html> -->` + last, last},
		{"trailing comment is ignored", last + `<!-- <html><body>example</body></html> -->`, last},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, extractScheduledTestHTML(tc.source)) })
	}
}

func TestExtractScheduledTestHTMLSelectsAcrossJSONCommands(t *testing.T) {
	first := `<html><body>first</body></html>`
	last := "<!doctype html>\n" + `<html><body><svg><circle r="20"/></svg><script>const path = "C:\\tmp"; const line = "a\nb";</script></body></html>`
	command := func(source string) string {
		var payload strings.Builder
		encoder := json.NewEncoder(&payload)
		encoder.SetEscapeHTML(false)
		require.NoError(t, encoder.Encode(map[string]string{"cmd": "cat <<'EOF'\n" + source + "\nEOF"}))
		return payload.String()
	}
	for _, source := range []string{
		first + "\nto=container.exec code:\n" + command(last),
		command(first) + "\nFinal HTML:\n" + last,
		command(first) + "\n" + command(last),
		command(first + "\n" + last),
		command(last) + "\n" + command(`<html><body><svg><text>unfinished</text></svg>`),
	} {
		require.Equal(t, last, extractScheduledTestHTML(source))
	}
	encoded, err := json.Marshal(map[string]string{"cmd": "cat <<'EOF'\n" + last + "\nEOF"})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `\u003c`)
	require.Equal(t, last, extractScheduledTestHTML(first+"\n"+string(encoded)), "JSON's default Unicode escapes must also decode exactly once")
	compact := `<html><body><script>const label = "ok";</script><svg/></body></html>`
	require.Equal(t, compact, extractScheduledTestHTML(command("<!-- generated -->\n"+compact)), "a leading comment must not hide the enclosing JSON string")
}
