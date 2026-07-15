package main

import (
	"bytes"
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

// captureStdout redirects the package stdout writer to a buffer for the test.
func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := stdout
	stdout = &buf
	t.Cleanup(func() { stdout = previous })
	return &buf
}

func TestVersionCommandWritesToStdout(t *testing.T) {
	out := captureStdout(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handleSubcommands(ctx, []string{"version"}); err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Jetty version") {
		t.Fatalf("expected version on stdout, got %q", out.String())
	}
}

func TestParseImageReference(t *testing.T) {
	cases := []struct {
		in         string
		repository string
		tag        string
		wantErr    bool
	}{
		{"repo", "repo", "latest", false},
		{"repo:tag", "repo", "tag", false},
		{"localhost:5000/img", "localhost:5000/img", "latest", false},
		{"localhost:5000/img:v1", "localhost:5000/img", "v1", false},
		{"  golang:1.20-alpine  ", "golang", "1.20-alpine", false},
		{"", "", "", true},
	}
	for _, c := range cases {
		box, err := parseImageReference(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseImageReference(%q) expected error, got %+v", c.in, box)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseImageReference(%q) unexpected error: %v", c.in, err)
			continue
		}
		if box.Repository != c.repository || box.Tag != c.tag {
			t.Errorf("parseImageReference(%q) = %s:%s, want %s:%s", c.in, box.Repository, box.Tag, c.repository, c.tag)
		}
	}
}

func TestParseGithubImport(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"github.com/o/r", "https://raw.githubusercontent.com/o/r/main/Jettyfile", false},
		{"github.com/o/r@v1.2.3", "https://raw.githubusercontent.com/o/r/v1.2.3/Jettyfile", false},
		{"github.com/o/r/sub/dir/File", "https://raw.githubusercontent.com/o/r/main/sub/dir/File", false},
		{"github.com/o", "", true},
		{"gitlab.com/o/r", "", false},
		{"./local.Jettyfile", "", false},
	}
	for _, c := range cases {
		got, err := parseGithubImport(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseGithubImport(%q) expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGithubImport(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseGithubImport(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsSubpath(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	sibling := filepath.Join(root, "sibling")
	for _, d := range []string{child, sibling} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		parent string
		child  string
		want   bool
	}{
		{root, child, true},
		{root, root, true},
		{child, root, false},
		{child, sibling, false},
	}
	for _, c := range cases {
		if got := isSubpath(c.parent, c.child); got != c.want {
			t.Errorf("isSubpath(%q, %q) = %v, want %v", c.parent, c.child, got, c.want)
		}
	}
}

func TestMatchesBuildFilter(t *testing.T) {
	info := BuildInfo{ID: "abc123", Status: "Failed", WorkerNode: "local", FileName: "/home/x/Jettyfile"}
	cases := []struct {
		filter string
		want   bool
	}{
		{"status=Failed", true},
		{"status=failed", true},
		{"id=abc123", true},
		{"id=nope", false},
		{"worker=local", true},
		{"worker_node=local", true},
		{"file=Jettyfile", true},
		{"filename=nope", false},
		{"bogus=x", false},
		{"abc123", true},
		{"nomatch", false},
	}
	for _, c := range cases {
		if got := matchesBuildFilter(info, c.filter); got != c.want {
			t.Errorf("matchesBuildFilter(%q) = %v, want %v", c.filter, got, c.want)
		}
	}
}

func TestLoadEnvFileParsing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(jettyStateDirEnv, filepath.Join(dir, "state"))
	envContent := strings.Join([]string{
		"# a comment",
		"",
		"FOO=bar",
		"QUOTED=\"hello world\"",
		"SINGLE='sq'",
		"WITHEQ=a=b",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0644); err != nil {
		t.Fatal(err)
	}
	buildFile := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"^FMT foo.txt \"%s\" $FOO",
		"^FMT quoted.txt \"%s\" $QUOTED",
		"^FMT single.txt \"%s\" $SINGLE",
		"^FMT witheq.txt \"%s\" $WITHEQ",
		"",
	}, "\n")
	if err := os.WriteFile(buildFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runBuildForTest(t, buildFile); err != nil {
		t.Fatalf("build returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "foo.txt"), "bar")
	assertFileContent(t, filepath.Join(dir, "quoted.txt"), "hello world")
	assertFileContent(t, filepath.Join(dir, "single.txt"), "sq")
	assertFileContent(t, filepath.Join(dir, "witheq.txt"), "a=b")
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
	// -f <file> --env-file <file> is four tokens; the pre-dispatch arg check must
	// not reject the documented flag combination before build parses it.
	if err := handleSubcommands(ctx, []string{"build", "-f", buildFile, "--env-file", envFile}); err != nil {
		t.Fatalf("handleSubcommands returned error: %v", err)
	}
	assertFileContent(t, filepath.Join(dir, "out.txt"), "fromenv")
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

func TestExecuteUseErrorPaths(t *testing.T) {
	newState := func() *BuildState {
		return &BuildState{
			Context: context.Background(),
			Args:    map[string]string{},
			Env:     map[string]string{},
			Boxes:   map[string]BoxInfo{},
		}
	}

	if err := executeUse(newState(), ""); err == nil || !strings.Contains(err.Error(), "USE requires a box name and command") {
		t.Fatalf("empty USE: unexpected error: %v", err)
	}

	if err := executeUse(newState(), "echo hi"); err == nil || !strings.Contains(err.Error(), "known box name") {
		t.Fatalf("USE without default box: unexpected error: %v", err)
	}

	state := newState()
	state.Boxes["mybox"] = BoxInfo{Repository: "alpine", Tag: "latest"}
	state.DefaultBox = "mybox"
	if err := executeUse(state, "mybox"); err == nil || !strings.Contains(err.Error(), "USE requires a command") {
		t.Fatalf("USE with box but no command: unexpected error: %v", err)
	}
}
