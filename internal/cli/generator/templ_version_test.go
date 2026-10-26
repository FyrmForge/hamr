package generator

import (
	"os"
	"regexp"
	"testing"
)

func TestTemplVersionMatchesGoMod(t *testing.T) {
	b, err := os.ReadFile("../../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*github\.com/a-h/templ (\S+)`).FindSubmatch(b)
	if m == nil {
		t.Fatal("github.com/a-h/templ not found in repo go.mod")
	}
	if got := string(m[1]); got != TemplVersion {
		t.Fatalf("repo go.mod has templ %s but generator.TemplVersion is %s; bump both together (templ CLI and runtime must match exactly)", got, TemplVersion)
	}
}
