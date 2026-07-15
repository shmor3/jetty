package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildUsesJettyfileDirectoryAndRunsSubBuild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	mainFile := filepath.Join(dir, "Jettyfile")
	subFile := filepath.Join(dir, "sub.Jettyfile")
	mainContent := strings.Join([]string{
		"ARG MSG=parent",
		"DIR out",
		"^FMT out/parent.txt \"%s\" $MSG",
		"SUB sub.Jettyfile",
		"WDR out",
		"DIR nested",
		"CMD echo done",
		"",
	}, "\n")
	subContent := strings.Join([]string{
		"&FMT CHILD \"%s\" child",
		"^FMT out/child.txt \"%s\" $CHILD",
		"",
	}, "\n")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(subFile, []byte(subContent), 0644); err != nil {
		t.Fatal(err)
	}

	output, infos, err := runBuildForTest(t, mainFile)
	if err != nil {
		t.Fatalf("build returned error: %v\noutput:\n%s", err, strings.Join(output, "\n"))
	}
	assertFileContent(t, filepath.Join(dir, "out", "parent.txt"), "parent")
	assertFileContent(t, filepath.Join(dir, "out", "child.txt"), "child")
	if _, err := os.Stat(filepath.Join(dir, "out", "nested")); err != nil {
		t.Fatalf("expected nested directory to exist: %v", err)
	}
	if len(infos) == 0 || infos[len(infos)-1].Status != statusCompleted {
		t.Fatalf("expected final build status Completed, got %#v", infos)
	}
	if !joinedOutputContains(output, "CMD: done") {
		t.Fatalf("expected CMD output, got %q", output)
	}
}

func TestBuildWaitsForAsyncCopyBeforeCMD(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("copied"), 0644); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"*CPY source.txt out/copied.txt",
		"CMD echo done",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("build returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "out", "copied.txt"), "copied")
}

func TestBuildCancelsAsyncWorkBeforeReturningOnSyncFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("sh"); err != nil {
			t.Skip("requires sh for portable sleep command")
		}
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"*RUN sleep 15",
		"RUN exit 9",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, infos, err := runBuildForTest(t, buildFile)
	if err == nil {
		t.Fatal("expected build to fail")
	}
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("expected async work to be cancelled promptly (within WaitDelay bounds), took %s", elapsed)
	}
	if len(infos) == 0 || infos[len(infos)-1].Status != statusFailed {
		t.Fatalf("expected final build status Failed, got %#v", infos)
	}
}

func TestCopyRejectsDirectoryIntoItself(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	if err := os.Mkdir(filepath.Join(dir, "source"), 0755); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("CPY source source/nested\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildForTest(t, buildFile)
	if err == nil {
		t.Fatal("expected recursive copy to fail")
	}
	if !strings.Contains(err.Error(), "cannot copy directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFormatCanSetArgsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"ENV PREFIX=env-prefix",
		"&FMT TARGET \"%s\" arg-value",
		"$FMT EXPORTED \"%s\" env-value",
		"^FMT $TARGET.txt \"%s\" $TARGET",
		"^FMT env.txt \"%s\" $PREFIX",
		"CMD echo $EXPORTED",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	output, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("build returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "arg-value.txt"), "arg-value")
	assertFileContent(t, filepath.Join(dir, "env.txt"), "env-prefix")
	if !joinedOutputContains(output, "CMD: env-value") {
		t.Fatalf("expected CMD to see formatted environment variable, got %q", output)
	}
}

func TestFormatRejectsInvalidVariableNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("&FMT 1BAD \"%s\" value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildForTest(t, buildFile)
	if err == nil {
		t.Fatal("expected invalid argument name to fail")
	}
	if !strings.Contains(err.Error(), "invalid argument name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginArgumentsAreExpanded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("plugin execution bit behavior is platform-specific")
	}
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pluginsDir, 0755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginsDir, "capture")
	plugin := "#!/bin/sh\nprintf '%s' \"$1\" > plugin-output.txt\n"
	if err := os.WriteFile(pluginPath, []byte(plugin), 0755); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"ARG VALUE=expanded",
		"JET capture $VALUE",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("build returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "plugin-output.txt"), "expanded")
}

func TestBuildFailurePropagatesErrorAndFailedStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("RUN exit 7\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, infos, err := runBuildForTest(t, buildFile)
	if err == nil {
		t.Fatal("expected failed RUN to return an error")
	}
	if !errors.Is(err, ErrBuildFailed) {
		t.Fatalf("expected ErrBuildFailed, got %v", err)
	}
	if len(infos) == 0 || infos[len(infos)-1].Status != statusFailed {
		t.Fatalf("expected final build status Failed, got %#v", infos)
	}
}

func TestBuildStatusIsPersistedAndFilterable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err := runBuildForTest(t, buildFile)
	if err != nil {
		t.Fatalf("build returned error: %v", err)
	}
	builds, err := readBuildInfos()
	if err != nil {
		t.Fatalf("readBuildInfos returned error: %v", err)
	}
	completed := filterBuildInfos(builds, true, "status=Completed")
	if len(completed) != 1 {
		t.Fatalf("expected one completed build, got %#v", completed)
	}
	active := filterBuildInfos(builds, false, "")
	if len(active) != 0 {
		t.Fatalf("expected no active builds, got %#v", active)
	}
}

func TestStatusCommandShowsCompletedBuildsByDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("build returned error: %v", err)
	}

	output := captureStdout(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"status"}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}

	statusOutput := output.String()
	baseFile := filepath.Base(buildFile)
	if !strings.Contains(statusOutput, "ID") || !strings.Contains(statusOutput, statusCompleted) || !strings.Contains(statusOutput, baseFile) {
		t.Fatalf("expected status history output containing %q, got %q", baseFile, statusOutput)
	}
}

func TestDefaultCommandShowsStatusHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("build returned error: %v", err)
	}

	output := captureStdout(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, nil); err != nil {
		t.Fatalf("default command returned error: %v", err)
	}
	if !strings.Contains(output.String(), statusCompleted) {
		t.Fatalf("expected default command to show status history, got %q", output.String())
	}
}

func TestHandleBuildCommandPreservesFileFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Custom.Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"build", "-f", buildFile}); err != nil {
		t.Fatalf("handleSubcommands returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out")); err != nil {
		t.Fatalf("expected build -f to use custom build file: %v", err)
	}
}

func TestBuildCommandRejectsAmbiguousFileArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := handleSubcommands(ctx, []string{"build", "-f", buildFile, buildFile})
	if err == nil {
		t.Fatal("expected ambiguous build file arguments to fail")
	}
	if !strings.Contains(err.Error(), "too many arguments") && !strings.Contains(err.Error(), "either -f or one positional file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPSCommandDoesNotBlockWithoutBuilds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"ps"}); err != nil {
		t.Fatalf("ps returned error: %v", err)
	}
}

func TestPSCommandGuidesToStatusWhenOnlyCompletedBuildsExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	buildFile := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(buildFile, []byte("DIR out\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("build returned error: %v", err)
	}

	output := captureLoggerOutput(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"ps"}); err != nil {
		t.Fatalf("ps returned error: %v", err)
	}
	if !strings.Contains(output.String(), "jetty status") {
		t.Fatalf("expected ps to guide to status history, got %q", output.String())
	}
}

func TestPSCommandRejectsPositionalArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := handleSubcommands(ctx, []string{"ps", "unexpected"})
	if err == nil {
		t.Fatal("expected ps positional argument to fail")
	}
	if !strings.Contains(err.Error(), "ps does not accept positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGlobalHelpUsesCustomUsage(t *testing.T) {
	output := captureLoggerOutput(t)

	customUsage()

	if !strings.Contains(output.String(), "Commands:") ||
		!strings.Contains(output.String(), "build") ||
		!strings.Contains(output.String(), "status") {
		t.Fatalf("expected custom usage to include commands, got %q", output.String())
	}
}

func captureLoggerOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previousLogger := logger
	logger = log.New(&output, "", 0)
	t.Cleanup(func() {
		logger = previousLogger
	})
	return &output
}

func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := stdout
	stdout = &output
	t.Cleanup(func() { stdout = previous })
	return &output
}

func runBuildForTest(t *testing.T, fileName string) ([]string, []BuildInfo, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resultChan := make(chan string)
	buildInfoChan := make(chan BuildInfo)
	errChan := make(chan error, 1)
	go func() {
		errChan <- build(ctx, fileName, "test-build", "test-worker", resultChan, buildInfoChan, "")
	}()

	var output []string
	var infos []BuildInfo
	resultOpen := true
	infoOpen := true
	for resultOpen || infoOpen {
		select {
		case result, ok := <-resultChan:
			if !ok {
				resultOpen = false
				continue
			}
			output = append(output, result)
		case info, ok := <-buildInfoChan:
			if !ok {
				infoOpen = false
				continue
			}
			infos = append(infos, info)
		case <-ctx.Done():
			t.Fatalf("build timed out: %v", ctx.Err())
		}
	}
	return output, infos, <-errChan
}

func assertFileContent(t *testing.T, fileName string, expected string) {
	t.Helper()
	data, err := os.ReadFile(fileName)
	if err != nil {
		t.Fatalf("failed to read %s: %v", fileName, err)
	}
	if string(data) != expected {
		t.Fatalf("%s = %q, want %q", fileName, string(data), expected)
	}
}

func joinedOutputContains(output []string, fragment string) bool {
	for _, line := range output {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

func TestLoadEnvFile(t *testing.T) {
	state := &BuildState{
		Env: make(map[string]string),
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "env")
	content := "A=B\nC=D\n#comment\n\nE=\"F\"\nG='H'"
	os.WriteFile(file, []byte(content), 0644)

	loadEnvFile(state, file)

	if state.Env["A"] != "B" || state.Env["C"] != "D" || state.Env["E"] != "F" || state.Env["G"] != "H" {
		t.Errorf("failed to load environment variables, got %v", state.Env)
	}

	loadEnvFile(state, "nonexistent.env")
}

func TestProcessBuildEdgeCases(t *testing.T) {
	// missing file name
	job := Job{}
	err := processBuild(job)
	if err == nil || !strings.Contains(err.Error(), "please provide a file name") {
		t.Errorf("expected missing file name error, got %v", err)
	}

	// exceeded depth
	job = Job{FileName: "dummy", Depth: 51}
	err = processBuild(job)
	if err == nil || !strings.Contains(err.Error(), "maximum sub-build depth exceeded") {
		t.Errorf("expected max depth error, got %v", err)
	}

	// default initialization check
	dir := t.TempDir()
	file := filepath.Join(dir, "Jettyfile")
	os.WriteFile(file, []byte(""), 0644)

	job = Job{FileName: file}
	// processBuild will return nil for an empty file, covering the defaults
	processBuild(job)
}

func TestLockStatusStoreErrors(t *testing.T) {
	// 1. MkdirAll failure by making the dir a file
	dir := t.TempDir()
	badDir := filepath.Join(dir, "baddir")
	os.WriteFile(badDir, []byte(""), 0644)
	os.Setenv("JETTY_STATE_DIR", badDir)
	defer os.Unsetenv("JETTY_STATE_DIR")

	_, err := lockStatusStore()
	if err == nil {
		t.Errorf("expected MkdirAll failure")
	}

	// 2. Lock timeout
	goodDir := filepath.Join(dir, "gooddir")
	os.Setenv("JETTY_STATE_DIR", goodDir)
	os.MkdirAll(goodDir, 0755)

	unlock, err := lockStatusStore()
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}

	// second lock should timeout after 5 seconds, wait that's long.
	// wait, the loop is 50 * 100ms = 5 seconds.
	// To speed it up, we can just run it in a goroutine or wait.
	// For test it's fine.
	_, err = lockStatusStore()
	if err == nil || !strings.Contains(err.Error(), "timeout waiting for lock") {
		t.Errorf("expected lock timeout, got %v", err)
	}

	unlock()
}

func TestBuildInfoErrors(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".jetty")
	os.Setenv("JETTY_STATE_DIR", stateDir)
	defer os.Unsetenv("JETTY_STATE_DIR")

	os.MkdirAll(stateDir, 0755)
	storePath := filepath.Join(stateDir, "builds.json")

	// 1. Invalid JSON read
	os.WriteFile(storePath, []byte("{bad json"), 0644)
	err := saveBuildInfo(BuildInfo{ID: "test"})
	if err == nil {
		t.Error("expected saveBuildInfo to fail on bad json")
	}

	_, err = readBuildInfos()
	if err == nil {
		t.Error("expected readBuildInfos to fail on bad json")
	}

	// 2. Cannot read file because it is a directory
	os.Remove(storePath)
	os.MkdirAll(storePath, 0755)

	err = saveBuildInfo(BuildInfo{ID: "test2"})
	if err == nil {
		t.Error("expected saveBuildInfo to fail when builds.json is a directory")
	}

	// 3. writeBuildInfosLocked failure
	os.Remove(storePath)
	// make stateDir readonly to cause CreateTemp to fail
	// on windows this might not work perfectly just using chmod, but let's try
	// wait, if we create a file at stateDir it fails MkdirAll in writeBuildInfosLocked
	os.RemoveAll(stateDir)
	os.WriteFile(stateDir, []byte(""), 0644)
	err = saveBuildInfo(BuildInfo{ID: "test3"})
	if err == nil {
		t.Error("expected saveBuildInfo to fail when stateDir is a file")
	}
}

func TestExecuteInstructionsEdgeCases(t *testing.T) {
	// Multiple CMD directives
	state := &BuildState{
		Context: context.Background(),
		Cancel:  func() {},
	}
	insts := []Instruction{
		{Directive: "CMD", Args: "foo"},
		{Directive: "CMD", Args: "bar"},
	}
	err := executeInstructions(state, insts)
	if err == nil || !strings.Contains(err.Error(), "multiple CMD") {
		t.Error("expected multiple CMD error")
	}

	// Context canceled early
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	state.Context = ctx
	insts = []Instruction{
		{Directive: "RUN", Args: "echo"},
	}
	err = executeInstructions(state, insts)
	if err == nil {
		t.Error("expected context canceled error from executeInstructions")
	}

	// Async Context canceled
	ctx2, cancel2 := context.WithCancel(context.Background())
	state2 := &BuildState{
		Context: ctx2,
		Cancel:  cancel2,
		WorkDir: ".",
		Args:    make(map[string]string),
		Env:     make(map[string]string),
	}
	// start a long async task, but cancel context
	cancel2()
	insts2 := []Instruction{
		{Directive: "RUN", Symbol: "*", Args: "sleep 10"},
	}
	err = executeInstructions(state2, insts2)
	if err == nil {
		t.Error("expected async error when context is cancelled")
	}
}

func TestPublishBuildInfoContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan BuildInfo)
	cancel()

	// This should return immediately because ctx is done,
	// rather than blocking forever on the unbuffered channel.
	publishBuildInfo(ctx, ch, BuildInfo{})
}

func TestPublishBuildInfoNilChan(t *testing.T) {
	publishBuildInfo(context.Background(), nil, BuildInfo{})
}

func TestPublishBuildInfoSaveError(t *testing.T) {
	// Cause saveBuildInfo to fail
	f, err := os.CreateTemp("", "bad-dir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	oldEnv := os.Getenv(jettyStateDirEnv)
	os.Setenv(jettyStateDirEnv, f.Name())
	defer os.Setenv(jettyStateDirEnv, oldEnv)

	// Since we mock the logger output, it just logs
	publishBuildInfo(context.Background(), nil, BuildInfo{})
}

func TestReadBuildInfosError(t *testing.T) {
	oldEnv := os.Getenv(jettyStateDirEnv)
	f, err := os.CreateTemp("", "bad-dir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	// Make lock fail or read fail
	// Actually, if we set JETTY_STATE_DIR to \x00invalid, os.ReadFile might fail. But wait, lockStatusStore creates the dir and it will fail.
	os.Setenv(jettyStateDirEnv, f.Name())
	defer os.Setenv(jettyStateDirEnv, oldEnv)

	_, err = readBuildInfos()
	if err == nil {
		t.Error("expected readBuildInfos to fail due to lock failure")
	}

	// Now make readBuildInfosLocked fail by making builds.json a directory
	dir := t.TempDir()
	os.Setenv(jettyStateDirEnv, dir)
	os.MkdirAll(filepath.Join(dir, "builds.json"), 0755)
	_, err = readBuildInfosLocked()
	if err == nil {
		t.Error("expected readBuildInfosLocked to fail due to builds.json being a directory")
	}
}
func TestSnapshot(t *testing.T) {
	state := &BuildState{
		Context: context.Background(),
		WorkDir: ".",
		Args:    map[string]string{"foo": "bar"},
		Env:     map[string]string{"A": "B"},
		Boxes:   map[string]BoxInfo{"box1": {Repository: "repo1"}},
	}

	snap := state.snapshot()
	if snap.Args["foo"] != "bar" {
		t.Error("snapshot did not clone Args")
	}
	if snap.Env["A"] != "B" {
		t.Error("snapshot did not clone Env")
	}
	if snap.Boxes["box1"].Repository != "repo1" {
		t.Error("snapshot did not clone Boxes")
	}
}

func TestWriteBuildInfosLockedErrors(t *testing.T) {
	// Set JETTY_STATE_DIR to a file so MkdirAll fails
	f, err := os.CreateTemp("", "bad-dir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	oldEnv := os.Getenv(jettyStateDirEnv)
	os.Setenv(jettyStateDirEnv, f.Name())
	defer os.Setenv(jettyStateDirEnv, oldEnv)

	err = writeBuildInfosLocked([]BuildInfo{})
	if err == nil {
		t.Error("expected writeBuildInfosLocked to fail with MkdirAll error")
	}

	// Now set to a valid dir, but make CreateTemp fail by making the dir read-only (Windows ACL is hard, maybe use an invalid name? On Windows, \0 is invalid)
	os.Setenv(jettyStateDirEnv, "\x00invalid")
	err = writeBuildInfosLocked([]BuildInfo{})
	if err == nil {
		t.Error("expected writeBuildInfosLocked to fail due to invalid dir")
	}

	// Now make os.Rename fail by making builds.json a directory
	validDir := t.TempDir()
	os.Setenv(jettyStateDirEnv, validDir)
	os.MkdirAll(filepath.Join(validDir, "builds.json"), 0755)
	err = writeBuildInfosLocked([]BuildInfo{})
	if err == nil {
		t.Error("expected writeBuildInfosLocked to fail when renaming over a directory")
	}
}
