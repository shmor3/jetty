package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBuildAcceptsEnvFileWithExplicitBuildFile exercises `build -f <file>
// --env-file <file>` (four tokens). The pre-dispatch arg check must not reject
// the documented flag combination before build parses it.
func TestBuildAcceptsEnvFileWithExplicitBuildFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Custom.Jettyfile")
	if err := os.WriteFile(buildFile, []byte("^FMT out.txt \"%s\" $FOO\n"), 0644); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(dir, "custom.env")
	if err := os.WriteFile(envFile, []byte("FOO=fromenv\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"build", "-f", buildFile, "--env-file", envFile}); err != nil {
		t.Fatalf("handleSubcommands returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "out.txt"), "fromenv")
}

func TestBuildMissingExplicitEnvFileFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := handleSubcommands(ctx, []string{"build", "-f", buildFile, "--env-file", filepath.Join(dir, "missing.env")})
	if err == nil {
		t.Fatal("expected missing explicit env file to fail the build")
	}
	if !strings.Contains(err.Error(), "failed to load env file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildAsyncFailureSuppressesCMD(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("requires sh for portable exit command")
		}
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"*RUN exit 1",
		"CMD echo SHOULD_NOT_RUN",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	output, infos, err := runBuildForTest(t, buildFile)
	if err == nil {
		t.Fatal("expected async failure to fail the build")
	}
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("expected ErrBuildFailed, got %v", err)
	}
	if joinedOutputContains(output, "SHOULD_NOT_RUN") {
		t.Fatalf("CMD must not run after an async failure, got output: %q", output)
	}
	if len(infos) == 0 || infos[len(infos)-1].Status != statusFailed {
		t.Fatalf("expected final build status Failed, got %#v", infos)
	}
}

func TestLineWriterDetachDropsWrites(t *testing.T) {
	ch := make(chan string, 8)
	state := &BuildState{Context: context.Background(), ResultChan: ch}
	lw := &lineWriter{label: "T", state: state}
	if _, err := lw.Write([]byte("before\n")); err != nil {
		t.Fatal(err)
	}
	lw.detach()
	// Writes and Close after detach must not send on the channel or panic.
	if _, err := lw.Write([]byte("after\n")); err != nil {
		t.Fatal(err)
	}
	if err := lw.Close(); err != nil {
		t.Fatal(err)
	}
	close(ch)
	var got []string
	for m := range ch {
		got = append(got, m)
	}
	if len(got) != 1 || !strings.Contains(got[0], "before") {
		t.Fatalf("expected only the pre-detach line, got %q", got)
	}
}

func TestUnreadableImplicitEnvDoesNotFailBuild(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("FOO=bar\n"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(envPath, 0644) })
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("an unreadable implicit .env must not fail the build, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err != nil {
		t.Fatalf("expected build to complete: %v", err)
	}
}
