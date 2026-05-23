package distill

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintAgentsGuideDocumentsWatcherStartup(t *testing.T) {
	var out bytes.Buffer

	printAgentsGuide(&out)
	got := out.String()

	for _, want := range []string{
		"distill agent guide",
		"Watch uses --new semantics with no default batch cap.",
		"On startup, distill watch immediately performs one extraction pass.",
		"~/.distill/watch.log",
		"Promotion is intentionally human-in-the-loop.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("agent guide missing %q:\n%s", want, got)
		}
	}
}
