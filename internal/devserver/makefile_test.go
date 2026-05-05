package devserver

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeMakefile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "Makefile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestMakefileTargetsFromPath_Basic(t *testing.T) {
	path := writeMakefile(t, `# top-level comment
build:
	go build ./...

test: build
	go test ./...

vet:
	go vet ./...
`)
	got, err := MakefileTargetsFromPath(path)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"build", "test", "vet"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_PreservesOrder(t *testing.T) {
	path := writeMakefile(t, `zebra:
	@true
alpha:
	@true
mango:
	@true
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"zebra", "alpha", "mango"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_SkipsVariablesAndPhonyAndPatterns(t *testing.T) {
	path := writeMakefile(t, `GO     := go
BIN    ?= bin/hamr
FLAGS   = -trimpath
.PHONY: build clean
%.o: %.c
	$(CC) -c $<
build:
	$(GO) build
clean:
	rm -rf $(BIN)
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"build", "clean"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_MultipleTargetsOneLine(t *testing.T) {
	path := writeMakefile(t, `build clean install: deps
	@echo
deps:
	@echo
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"build", "clean", "install", "deps"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_StripsInlineComments(t *testing.T) {
	path := writeMakefile(t, `build: # build it
	@true
test: build  # also depends
	@true
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"build", "test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_DedupesRepeatedTargetDecls(t *testing.T) {
	// Make permits redefining a target's recipe; we list each name only
	// once so the run overlay doesn't show duplicate rows.
	path := writeMakefile(t, `build:
	@true
build:
	@true
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"build"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMakefileTargetsFromPath_MissingFileReturnsEmptyNoError(t *testing.T) {
	got, err := MakefileTargetsFromPath(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestMakefileTargetsFromPath_IgnoresRecipeLines(t *testing.T) {
	// Lines starting with a tab are recipes — they may contain ':' (e.g.
	// a shell command) and must never be parsed as targets.
	path := writeMakefile(t, `build:
	echo "hello: world"
	docker run alpine sh -c 'echo a:b'
`)
	got, _ := MakefileTargetsFromPath(path)
	want := []string{"build"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
