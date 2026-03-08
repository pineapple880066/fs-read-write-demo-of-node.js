package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGoASTSummaryIncludesKeySymbols(t *testing.T) {
	src := []byte(`package demo

import "context"

type Config struct {
	Name string
}

func Build(ctx context.Context, cfg Config) error { return nil }

type Runner struct{}

func (r *Runner) Run() {}
`)

	summary := buildGoASTSummary("demo.go", src)
	if !strings.Contains(summary, "package: demo") {
		t.Fatalf("expected package in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "Config struct") {
		t.Fatalf("expected struct type in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "Build(ctx context.Context, cfg Config) error") {
		t.Fatalf("expected function signature in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "(*Runner).Run()") {
		t.Fatalf("expected method signature in summary, got: %s", summary)
	}
}

func TestInferVerificationCommandPrefersGoAndTypeScript(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := inferVerificationCommand(root, []string{"internal/service/service.go"}); got != "go test ./..." {
		t.Fatalf("expected go verification command, got %q", got)
	}

	tsRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(tsRoot, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := inferVerificationCommand(tsRoot, []string{"src/app.ts"}); got != "npx tsc --noEmit" {
		t.Fatalf("expected ts verification command, got %q", got)
	}
}

func TestIsAllowedVerificationCommandRejectsShellChains(t *testing.T) {
	if hasShellMeta("go test ./... && rm -rf /tmp/x") {
		// expected path
	} else {
		t.Fatalf("expected shell meta detection to reject chained command")
	}

	if !isAllowedVerificationCommand([]string{"go", "test", "./..."}) {
		t.Fatalf("expected go test to be allowed")
	}
	if isAllowedVerificationCommand([]string{"bash", "-lc", "go test ./..."}) {
		t.Fatalf("expected bash shell command to be rejected")
	}
}

func TestBuildTaskPlanDetectsTargetFileAndRanges(t *testing.T) {
	root := t.TempDir()
	servicePath := filepath.Join(root, "internal", "service")
	if err := os.MkdirAll(servicePath, 0755); err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	for i := 0; i < 480; i++ {
		content.WriteString("package service\n")
	}
	fullPath := filepath.Join(servicePath, "service.go")
	if err := os.WriteFile(fullPath, []byte(content.String()), 0644); err != nil {
		t.Fatal(err)
	}

	req := ChatRequest{
		Message: "你帮我在 internal/service/service.go 加上注释",
		Mode:    "chat",
		RootDir: root,
	}
	plan := buildTaskPlan(req, root, []string{"internal/service/service.go"})
	if plan.Route != "edit_file" {
		t.Fatalf("expected edit_file route, got %q", plan.Route)
	}
	if len(plan.TargetFiles) != 1 || plan.TargetFiles[0] != "internal/service/service.go" {
		t.Fatalf("unexpected target files: %+v", plan.TargetFiles)
	}
	if len(plan.MandatoryRanges["internal/service/service.go"]) == 0 {
		t.Fatalf("expected mandatory ranges for target file")
	}
}

func TestInspectionCoverageRequiresAllMandatoryRanges(t *testing.T) {
	required := []lineRange{{Start: 1, End: 100}, {Start: 101, End: 200}}
	state := newInspectionState(taskPlan{TargetFiles: []string{"service.go"}})
	markFileRangeRead(state, "service.go", lineRange{Start: 1, End: 100})
	if hasReadCoverage(state, "service.go", required) {
		t.Fatalf("expected partial coverage to be insufficient")
	}
	markFileRangeRead(state, "service.go", lineRange{Start: 101, End: 200})
	if !hasReadCoverage(state, "service.go", required) {
		t.Fatalf("expected full mandatory coverage")
	}
}
