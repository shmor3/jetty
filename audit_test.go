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

// TestBuildCacheSkipsUnchangedStep drives a DEP/OUT-cached RUN twice with
// unchanged inputs and asserts the second run is reported CACHED and does not
// re-execute the command (its side effect happens exactly once).
func TestBuildCacheSkipsUnchangedStep(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("requires sh for the RUN command")
		}
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0644); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"DEP input.txt",
		"OUT counter.txt",
		"RUN echo x >> counter.txt",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("first build failed: %v", err)
	}
	output, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	if !joinedOutputContains(output, "CACHED") {
		t.Fatalf("expected the second build to report a cache hit, got: %q", output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "counter.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "x"); got != 1 {
		t.Fatalf("expected the RUN to execute exactly once (counter=1), got %d:\n%q", got, string(data))
	}
}

func TestCopyDirPreservesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "real.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(src, "link.txt")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(context.Background(), src, dst); err != nil {
		t.Fatalf("copyDir must not fail on a tree containing a symlink: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(dst, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("expected the copied entry to remain a symlink")
	}
	assertFileContent(t, filepath.Join(dst, "real.txt"), "hi")
}

func TestCopyDirReadOnlySource(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	src := t.TempDir()
	sub := filepath.Join(src, "ro")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0755) })
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(context.Background(), src, dst); err != nil {
		t.Fatalf("copyDir must handle a read-only source directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "ro"), 0755) })
	assertFileContent(t, filepath.Join(dst, "ro", "f.txt"), "x")
}

func TestCopyDirReCopyIntoReadOnlyDest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/permission semantics differ on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores directory write permissions")
	}
	src := t.TempDir()
	sub := filepath.Join(src, "ro")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "real.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(sub, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0755) })
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(context.Background(), src, dst); err != nil {
		t.Fatalf("first copy failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dst, "ro"), 0755) })
	// Re-copying into the same (now read-only) destination must also succeed,
	// including replacing the symlink (which requires directory write access).
	if err := copyDir(context.Background(), src, dst); err != nil {
		t.Fatalf("re-copy into a read-only destination failed: %v", err)
	}
	assertFileContent(t, filepath.Join(dst, "ro", "real.txt"), "x")
}

func TestCopyFileRemovesPartialOnError(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("relies on reading a directory fd returning an error")
	}
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "srcdir")
	if err := os.Mkdir(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out", "dst.txt")
	if err := copyFile(context.Background(), srcDir, dst); err == nil {
		t.Fatal("expected copyFile to fail when the source is a directory")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("expected the partial destination to be removed, stat err: %v", err)
	}
}

// TestBuildCacheInvalidatesOnArgChange verifies that changing an ARG referenced
// by a cached command invalidates the cache and re-runs the step.
func TestBuildCacheInvalidatesOnArgChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("requires sh for the RUN command")
		}
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("input"), 0644); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	write := func(mode string) {
		content := strings.Join([]string{
			"ARG MODE=" + mode,
			"DEP input.txt",
			"OUT counter.txt",
			"RUN echo $MODE >> counter.txt",
			"",
		}, "\n")
		if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("debug")
	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("first build failed: %v", err)
	}
	write("release")
	output, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("second build failed: %v", err)
	}
	if joinedOutputContains(output, "CACHED") {
		t.Fatalf("changing an ARG must invalidate the cache, but the step was CACHED: %q", output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "counter.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "debug") || !strings.Contains(string(data), "release") {
		t.Fatalf("expected both runs to execute, got %q", string(data))
	}
}

// TestCacheKeyExcludesRuntimeIDs verifies the cache key ignores the per-run
// BUILD_ID/WORKER_NODE identifiers, so a command that references them still caches.
func TestCacheKeyExcludesRuntimeIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.WriteFile(filepath.Join(dir, "dep.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	inst := Instruction{Directive: "RUN", Args: "echo $BUILD_ID"}
	keyFor := func(buildID string) string {
		s := &BuildState{
			Context:     context.Background(),
			WorkDir:     dir,
			Env:         map[string]string{},
			Args:        map[string]string{"BUILD_ID": buildID, "WORKER_NODE": "local"},
			PendingDeps: []string{"dep.txt"},
		}
		if _, err := checkCache(s, inst); err != nil {
			t.Fatal(err)
		}
		return s.CurrentCacheKey
	}
	if k1, k2 := keyFor("111"), keyFor("222"); k1 != k2 {
		t.Fatalf("cache key must not depend on BUILD_ID: %q != %q", k1, k2)
	}
}

// TestAsyncSubBuildsDoNotStall spawns more concurrent async *SUB directives than
// there are semaphore slots, each with its own async child, and asserts the
// build completes. Without the SUB semaphore-skip this deadlocks until timeout.
func TestAsyncSubBuildsDoNotStall(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("requires sh for the RUN command")
		}
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.WriteFile(filepath.Join(dir, "child.Jettyfile"), []byte("*RUN echo hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 0; i < runtime.NumCPU()+2; i++ {
		lines = append(lines, "*SUB child.Jettyfile")
	}
	lines = append(lines, "CMD echo done", "")
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}

	output, infos, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("nested async sub-builds should complete without stalling, got: %v", err)
	}
	if !joinedOutputContains(output, "CMD: done") {
		t.Fatalf("expected CMD to run after all async sub-builds, got: %q", output)
	}
	if len(infos) == 0 || infos[len(infos)-1].Status != statusCompleted {
		t.Fatalf("expected final build status Completed, got %#v", infos)
	}
}
