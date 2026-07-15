package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseGithubImport(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"github.com/owner/repo", "https://raw.githubusercontent.com/owner/repo/main/Jettyfile", false},
		{"github.com/owner/repo@v1", "https://raw.githubusercontent.com/owner/repo/v1/Jettyfile", false},
		{"github.com/owner/repo@v1/path/to/Jettyfile", "https://raw.githubusercontent.com/owner/repo/v1/path/to/Jettyfile", false},
		{"github.com/owner/repo/path/to/Jettyfile", "https://raw.githubusercontent.com/owner/repo/main/path/to/Jettyfile", false},
		{"invalid", "", false},
		{"github.com/", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := parseGithubImport(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("parseGithubImport(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
				return
			}
			if result != tc.expected {
				t.Errorf("parseGithubImport(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}

func TestExecuteBoxAndUse(t *testing.T) {
	state := &BuildState{
		Context:    context.Background(),
		WorkDir:    ".",
		Args:       make(map[string]string),
		Env:        make(map[string]string),
		Boxes:      make(map[string]BoxInfo),
		ResultChan: make(chan string, 100),
	}

	// Test executeBox
	err := executeBox(state, "mybox ubuntu:latest")
	if err != nil {
		t.Fatalf("executeBox failed: %v", err)
	}
	if state.DefaultBox != "mybox" {
		t.Errorf("expected default box to be 'mybox', got %q", state.DefaultBox)
	}
	if box, ok := state.Boxes["mybox"]; !ok || box.Repository != "ubuntu" || box.Tag != "latest" {
		t.Errorf("box not correctly registered: %v", box)
	}

	// Test executeBox invalid args
	err = executeBox(state, "mybox")
	if err == nil {
		t.Error("expected error for missing image name in FRM")
	}

	// Note: We don't fully test executeUse because it spins up real Docker containers
	// but we can test invalid executeUse invocations
	err = executeUse(state, "")
	if err == nil {
		t.Error("expected error for missing command in USE")
	}

	// Only run if docker is available
	if os.Getenv("CI") == "" {
		state.WorkDir = "/tmp/workspace"
		err = executeUse(state, "mybox echo 'hello'")
		if err != nil {
			t.Errorf("executeUse failed: %v", err)
		}
	}
}

func TestParseImageReference(t *testing.T) {
	box, err := parseImageReference("ubuntu")
	if err != nil {
		t.Fatalf("parseImageReference failed: %v", err)
	}
	if box.Repository != "ubuntu" || box.Tag != "latest" {
		t.Errorf("expected ubuntu:latest, got %s:%s", box.Repository, box.Tag)
	}

	box, err = parseImageReference("ubuntu:20.04")
	if err != nil {
		t.Fatalf("parseImageReference failed: %v", err)
	}
	if box.Repository != "ubuntu" || box.Tag != "20.04" {
		t.Errorf("expected ubuntu:20.04, got %s:%s", box.Repository, box.Tag)
	}

	_, err = parseImageReference(" ")
	if err == nil {
		t.Error("expected parseImageReference to fail for empty string")
	}

	box, err = parseImageReference("myrepo.com:5000/image:tag")
	if err != nil || box.Repository != "myrepo.com:5000/image" || box.Tag != "tag" {
		t.Errorf("expected registry/image:tag, got %v, err %v", box, err)
	}
}

func TestFormatEnv(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
	}
	formatted := formatEnv(env)
	if len(formatted) != 1 || formatted[0] != "FOO=bar" {
		t.Errorf("expected [FOO=bar], got %v", formatted)
	}
}

func TestExecutePlugin(t *testing.T) {
	state := &BuildState{
		Context:    context.Background(),
		WorkDir:    ".",
		BaseDir:    ".",
		Args:       make(map[string]string),
		Env:        make(map[string]string),
		ResultChan: make(chan string, 100),
	}

	// Test missing plugin name
	err := executePlugin(state, "")
	if err == nil {
		t.Error("expected error for missing plugin name")
	}

	// Test nonexistent plugin
	err = executePlugin(state, "nonexistent")
	if err == nil || (!strings.Contains(err.Error(), "executable file not found") && !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "cannot find the file")) {
		t.Errorf("expected 'executable file not found', got %v", err)
	}

	// Test valid .bat on Windows
	if runtime.GOOS == "windows" {
		dir := t.TempDir()
		pluginsDir := filepath.Join(dir, "plugins")
		os.MkdirAll(pluginsDir, 0755)
		batFile := filepath.Join(pluginsDir, "myplugin.bat")
		os.WriteFile(batFile, []byte("@echo hello"), 0755)
		state.BaseDir = dir
		state.WorkDir = dir
		
		err = executePlugin(state, "myplugin")
		if err != nil {
			t.Errorf("expected bat plugin to execute, got %v", err)
		}
	}
}

func TestExecInContainer(t *testing.T) {
	// Only run if docker is available
	if os.Getenv("CI") != "" {
		t.Skip("Skipping docker test in CI")
	}

	workDir := "/tmp/workspace"

	state := &BuildState{
		Context:    context.Background(),
		WorkDir:    workDir,
		Args:       make(map[string]string),
		Env:        make(map[string]string),
		ResultChan: make(chan string, 100),
	}

	box := BoxInfo{
		Repository: "alpine",
		Tag:        "latest",
	}

	err := execInContainer(state.Context, "echo 'hello from container'", state.Env, box, state.WorkDir, state)
	if err != nil {
		t.Fatalf("execInContainer failed: %v", err)
	}
}

func TestExecuteInstruction(t *testing.T) {
	state := &BuildState{
		Context:    context.Background(),
		WorkDir:    t.TempDir(),
		Args:       make(map[string]string),
		Env:        make(map[string]string),
		ResultChan: make(chan string, 100),
		Boxes:      make(map[string]BoxInfo),
	}

	// Test ARG
	err := executeInstruction(state, Instruction{Directive: "ARG", Args: "TEST_ARG=123"})
	if err != nil {
		t.Errorf("executeInstruction ARG failed: %v", err)
	}
	if state.Args["TEST_ARG"] != "123" {
		t.Errorf("expected ARG to be set")
	}

	// Test ENV
	err = executeInstruction(state, Instruction{Directive: "ENV", Args: "TEST_ENV=456"})
	if err != nil {
		t.Errorf("executeInstruction ENV failed: %v", err)
	}
	if state.Env["TEST_ENV"] != "456" {
		t.Errorf("expected ENV to be set")
	}

	// Test DIR
	err = executeInstruction(state, Instruction{Directive: "DIR", Args: "mydir"})
	if err != nil {
		t.Errorf("executeInstruction DIR failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(state.WorkDir, "mydir")); err != nil {
		t.Errorf("expected DIR to create directory")
	}

	// Test WDR
	err = executeInstruction(state, Instruction{Directive: "WDR", Args: "mydir"})
	if err != nil {
		t.Errorf("executeInstruction WDR failed: %v", err)
	}
	if filepath.Base(state.WorkDir) != "mydir" {
		t.Errorf("expected WDR to change WorkDir")
	}

	// Test WDR failure
	err = executeInstruction(state, Instruction{Directive: "WDR", Args: "nonexistent"})
	if err == nil {
		t.Errorf("expected WDR to fail for nonexistent directory")
	}

	// Test FRM
	err = executeInstruction(state, Instruction{Directive: "FRM", Args: "ubuntu:latest"})
	if err != nil {
		t.Errorf("executeInstruction FRM failed: %v", err)
	}
	if state.DefaultBox != "default" {
		t.Errorf("expected FRM to set DefaultBox to default")
	}

	// Test SUB remote failure (404)
	err = executeInstruction(state, Instruction{Directive: "SUB", Args: "github.com/this-repo-should-not-exist/hopefully-ever"})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("expected SUB to fail with HTTP 404, got: %v", err)
	}

	// Test BOX with wrong args
	err = executeInstruction(state, Instruction{Directive: "BOX", Args: "mybox"})
	if err == nil {
		t.Errorf("expected BOX to fail with missing args")
	}

	// Test USE with no default box
	state.DefaultBox = ""
	err = executeInstruction(state, Instruction{Directive: "USE", Args: "some-command"})
	if err == nil {
		t.Errorf("expected USE to fail with no box name")
	}

	// Test BOX with parseImageReference failure
	err = executeInstruction(state, Instruction{Directive: "BOX", Args: "mybox  "})
	if err == nil {
		t.Errorf("expected BOX to fail for invalid image")
	}

	// Test BOX with 3 arguments
	err = executeInstruction(state, Instruction{Directive: "BOX", Args: "mybox myrepo mytag"})
	if err != nil {
		t.Errorf("expected BOX with 3 args to succeed, got: %v", err)
	}

	// Test BOX splitArgs failure
	err = executeInstruction(state, Instruction{Directive: "BOX", Args: "\"mybox"})
	if err == nil {
		t.Errorf("expected BOX to fail for unbalanced quotes")
	}
	// Test USE with default box but invalid fallback command? Actually USE with box doesn't check command validity immediately.
	// But let's check USE with explicit box when there's no command:
	state.Boxes["mybox"] = BoxInfo{Repository: "ubuntu", Tag: "latest"}
	state.DefaultBox = "mybox"
	// if I say USE mybox, the command is empty
	err = executeInstruction(state, Instruction{Directive: "USE", Args: "mybox "})
	if err == nil {
		t.Errorf("expected USE to fail with missing command")
	}

	// Test USE with non-existent image to trigger dockertest error
	state.Boxes["badbox"] = BoxInfo{Repository: "nonexistent-image", Tag: "badtag"}
	err = executeInstruction(state, Instruction{Directive: "USE", Args: "badbox echo 1"})
	if err == nil {
		t.Errorf("expected USE to fail with invalid container image")
	}


	// Test unknown directive
	err = executeInstruction(state, Instruction{Directive: "UNKNOWN", Args: ""})
	if err == nil {
		t.Errorf("expected unknown directive to fail")
	}

	// Test USE with canceled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	state.Context = cancelCtx
	state.Boxes["mybox"] = BoxInfo{Repository: "ubuntu", Tag: "latest"}
	state.DefaultBox = "mybox"
	err = executeInstruction(state, Instruction{Directive: "USE", Args: "mybox echo 1"})
	if err == nil {
		t.Errorf("expected USE to fail with canceled context")
	}

	// Test FMT edge cases
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "", Args: "\"broken"})
	if err == nil {
		t.Errorf("expected FMT to fail with broken quotes")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "", Args: ""})
	if err == nil {
		t.Errorf("expected FMT to fail without format string")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "^", Args: "file.txt"})
	if err == nil {
		t.Errorf("expected ^FMT to fail with < 2 parts")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "$", Args: "envvar"})
	if err == nil {
		t.Errorf("expected $FMT to fail with < 2 parts")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "$", Args: "123bad value"})
	if err == nil {
		t.Errorf("expected $FMT to fail with invalid name")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "&", Args: "argname"})
	if err == nil {
		t.Errorf("expected &FMT to fail with < 2 parts")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "&", Args: "123bad value"})
	if err == nil {
		t.Errorf("expected &FMT to fail with invalid name")
	}
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "!", Args: "fmt string"})
	if err == nil {
		t.Errorf("expected FMT to fail with unsupported modifier")
	}

	// Test ^FMT append error
	fileForErr := filepath.Join(state.WorkDir, "file_as_dir")
	os.WriteFile(fileForErr, []byte(""), 0644)
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "^", Args: "file_as_dir/child.txt %s hello"})
	if err == nil {
		t.Errorf("expected ^FMT to fail when appending to invalid path")
	}
	// Test FMT
	err = executeInstruction(state, Instruction{Directive: "FMT", Symbol: "^", Args: "test.txt %s hello"})
	if err != nil {
		t.Errorf("executeInstruction FMT failed: %v", err)
	}

	// Test WDR when path is a file
	fileAsDir := filepath.Join(state.WorkDir, "file_not_dir")
	os.WriteFile(fileAsDir, []byte("test"), 0644)
	err = executeInstruction(state, Instruction{Directive: "WDR", Args: fileAsDir})
	if err == nil {
		t.Errorf("expected WDR to fail for file")
	}

	// Test ARG invalid
	err = executeInstruction(state, Instruction{Directive: "ARG", Args: "invalid"})
	if err == nil {
		t.Errorf("expected ARG to fail with invalid format")
	}

	// Test JET errors
	err = executeInstruction(state, Instruction{Directive: "JET", Args: ""})
	if err == nil {
		t.Errorf("expected JET to fail with missing name")
	}
	err = executeInstruction(state, Instruction{Directive: "JET", Args: "\"broken"})
	if err == nil {
		t.Errorf("expected JET to fail with broken quotes")
	}
	
	// Test ENV invalid
	err = executeInstruction(state, Instruction{Directive: "ENV", Args: "invalid"})
	if err == nil {
		t.Errorf("expected ENV to fail for invalid assignment")
	}

	// Test DIR MkdirAll failure
	err = executeInstruction(state, Instruction{Directive: "DIR", Args: filepath.Join(fileAsDir, "bad")})
	if err == nil {
		t.Errorf("expected DIR to fail when MkdirAll fails")
	}

	// Test FRM invalid
	err = executeInstruction(state, Instruction{Directive: "FRM", Args: " "})
	if err == nil {
		t.Errorf("expected FRM to fail for empty args")
	}
}

func TestDirectivesHelpers(t *testing.T) {
	dir := t.TempDir()
	state := &BuildState{
		Context: context.Background(),
		WorkDir: dir,
		Args:    make(map[string]string),
		Env:     make(map[string]string),
	}

	// parseAssignment errors
	_, _, err := parseAssignment("bad", "ARG")
	if err == nil {
		t.Error("expected parseAssignment to fail for 'bad'")
	}
	_, _, err = parseAssignment("1bad=val", "ARG")
	if err == nil {
		t.Error("expected parseAssignment to fail for invalid name")
	}

	// splitArgs errors
	_, err = splitArgs("unclosed 'quote")
	if err == nil {
		t.Error("expected splitArgs to fail for unclosed quote")
	}

	// singlePath errors
	_, err = state.singlePath("", "DIR")
	if err == nil {
		t.Error("expected singlePath to fail for empty")
	}
	_, err = state.singlePath("a b", "DIR")
	if err == nil {
		t.Error("expected singlePath to fail for multiple paths")
	}

	// isSubpath checks
	if isSubpath(dir, filepath.Join(dir, "..")) {
		t.Error("expected isSubpath to return false for outside traversal")
	}
	// fake absolute path not in dir
	if isSubpath(dir, filepath.Join(filepath.Dir(dir), "other")) {
		t.Error("expected isSubpath to return false for outside absolute path")
	}

	// resolvePath
	if !filepath.IsAbs(state.resolvePath("/foo")) && !filepath.IsAbs(state.resolvePath("C:\\foo")) {
		t.Error("expected resolvePath to handle absolute path")
	}
	if !strings.HasPrefix(state.resolvePath("foo"), filepath.Clean(state.WorkDir)) {
		t.Error("expected resolvePath to handle relative path correctly")
	}

	// executeInstruction ARG failure
	err = executeInstruction(state, Instruction{Directive: "ARG", Args: "bad"})
	if err == nil {
		t.Error("expected executeInstruction ARG to fail")
	}

	// executeInstruction ENV failure
	err = executeInstruction(state, Instruction{Directive: "ENV", Args: "bad"})
	if err == nil {
		t.Error("expected executeInstruction ENV to fail")
	}

	// executeInstruction DIR failure (multiple paths)
	err = executeInstruction(state, Instruction{Directive: "DIR", Args: "a b"})
	if err == nil {
		t.Error("expected executeInstruction DIR to fail")
	}

	// executeInstruction WDR failure (multiple paths)
	err = executeInstruction(state, Instruction{Directive: "WDR", Args: "a b"})
	if err == nil {
		t.Error("expected executeInstruction WDR to fail")
	}
}

func TestExecuteSubBuild(t *testing.T) {
	dir := t.TempDir()
	subFile := filepath.Join(dir, "SubJettyfile")
	err := os.WriteFile(subFile, []byte("ARG TEST_SUB=1\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	state := &BuildState{
		Context:       context.Background(),
		WorkDir:       dir,
		BaseDir:       dir,
		Args:          make(map[string]string),
		Env:           make(map[string]string),
		ResultChan:    make(chan string, 100),
	}

	err = executeSubBuild(state, "SubJettyfile")
	if err != nil {
		t.Fatalf("executeSubBuild failed: %v", err)
	}

	err = executeSubBuild(state, "NonexistentFile")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestExecuteSubBuildGithub(t *testing.T) {
	state := &BuildState{
		Context: context.Background(),
		WorkDir: t.TempDir(),
		BaseDir: t.TempDir(),
		Args:    make(map[string]string),
		Env:     make(map[string]string),
		ResultChan: make(chan string, 100),
	}

	err := executeSubBuild(state, "github.com/shmor3/jetty/README.md")
	if err == nil || !strings.Contains(err.Error(), "invalid directive") {
		t.Fatalf("expected github fetch to succeed and fail at parse step, got %v", err)
	}

	err = executeSubBuild(state, "github.com/golang/go@badref/README.md")
	if err == nil {
		t.Error("expected github fetch to fail for bad ref")
	}

	err = executeSubBuild(state, "github.com/invalid")
	if err == nil {
		t.Error("expected parse error for invalid github import")
	}
}

func TestExecuteFormat(t *testing.T) {
	state := &BuildState{
		Context: context.Background(),
		WorkDir: t.TempDir(),
		Args:    make(map[string]string),
		Env:     make(map[string]string),
	}

	err := executeFormat(state, Instruction{Symbol: "^", Args: "file.txt %s foo"})
	if err != nil {
		t.Error("expected ^FMT to succeed")
	}
	err = executeFormat(state, Instruction{Symbol: "^", Args: "file.txt"})
	if err == nil {
		t.Error("expected ^FMT to fail for short args")
	}

	err = executeFormat(state, Instruction{Symbol: "$", Args: "1VAR %s foo"})
	if err == nil {
		t.Error("expected $FMT to fail for invalid name")
	}
	err = executeFormat(state, Instruction{Symbol: "$", Args: "VAR"})
	if err == nil {
		t.Error("expected $FMT to fail for short args")
	}

	err = executeFormat(state, Instruction{Symbol: "&", Args: "1VAR %s foo"})
	if err == nil {
		t.Error("expected &FMT to fail for invalid name")
	}
	err = executeFormat(state, Instruction{Symbol: "&", Args: "VAR"})
	if err == nil {
		t.Error("expected &FMT to fail for short args")
	}

	err = executeFormat(state, Instruction{Symbol: "*", Args: "something"})
	if err == nil {
		t.Error("expected *FMT to fail (unsupported)")
	}
}

func TestIsSubpath(t *testing.T) {
	if !isSubpath("C:\\foo", "C:\\foo\\bar") && runtime.GOOS == "windows" {
		t.Error("expected C:\\foo\\bar to be subpath of C:\\foo")
	}
	if isSubpath("C:\\foo", "D:\\bar") && runtime.GOOS == "windows" {
		t.Error("expected different drives to fail isSubpath")
	}
	if isSubpath("foo", "\x00invalid") { // Invalid byte might cause Abs to fail
		t.Error("expected invalid path to fail")
	}
}

func TestExecuteCopy(t *testing.T) {
	dir := t.TempDir()
	state := &BuildState{
		Context: context.Background(),
		WorkDir: dir,
		BaseDir: dir,
		Args:    make(map[string]string),
		Env:     make(map[string]string),
	}

	srcFile := filepath.Join(dir, "src.txt")
	os.WriteFile(srcFile, []byte("hello"), 0644)
	srcDir := filepath.Join(dir, "srcdir")
	os.Mkdir(srcDir, 0755)

	// Valid file copy
	err := executeCopy(state, "src.txt dst.txt")
	if err != nil {
		t.Errorf("executeCopy failed: %v", err)
	}

	// Valid dir copy
	err = executeCopy(state, "srcdir dstdir")
	if err != nil {
		t.Errorf("executeCopy failed: %v", err)
	}

	// Invalid unclosed quote
	err = executeCopy(state, "unclosed'quote dst.txt")
	if err == nil {
		t.Error("expected executeCopy to fail for unclosed quote")
	}

	// Invalid number of args
	err = executeCopy(state, "src.txt")
	if err == nil {
		t.Error("expected executeCopy to fail for single arg")
	}

	// Nonexistent source
	err = executeCopy(state, "nonexistent.txt dst.txt")
	if err == nil {
		t.Error("expected executeCopy to fail for nonexistent source")
	}

	// Subpath copy
	err = executeCopy(state, "srcdir srcdir/sub")
	if err == nil {
		t.Error("expected executeCopy to fail for copying dir into itself")
	}
}
