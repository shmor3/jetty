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

	output := captureLoggerOutput(t)
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

	output := captureLoggerOutput(t)
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

func runBuildForTest(t *testing.T, fileName string) ([]string, []BuildInfo, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resultChan := make(chan string)
	buildInfoChan := make(chan BuildInfo)
	errChan := make(chan error, 1)
	go func() {
		errChan <- build(ctx, fileName, "test-build", "test-worker", resultChan, buildInfoChan)
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
