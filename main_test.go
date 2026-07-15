package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	os.Setenv("JETTY_TIMEOUT", "invalid")
	initApp()
	os.Setenv("JETTY_TIMEOUT", "5s")
	initApp()
}

func TestShowCommandHelp(t *testing.T) {
	// Setup a dummy command
	dummyCmd := Command{
		Name:        "dummy",
		Description: "A dummy command for testing",
		Usage:       "dummy [args]",
		Flags:       flag.NewFlagSet("dummy", flag.ContinueOnError),
		Subcommands: map[string]*Command{
			"sub": {
				Name:        "sub",
				Description: "A dummy subcommand",
				Usage:       "dummy sub",
			},
		},
	}
	registerCommand("dummy", dummyCmd)

	// Capture logger output
	var buf bytes.Buffer
	oldLogger := logger
	logger = log.New(&buf, "", 0)
	defer func() { logger = oldLogger }()

	err := showCommandHelp("dummy")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "Usage:") || !strings.Contains(output, "Description:") {
		t.Errorf("output does not contain expected help strings: %s", output)
	}
	if !strings.Contains(output, "Subcommands:") || !strings.Contains(output, "A dummy subcommand") {
		t.Errorf("output does not contain subcommand help: %s", output)
	}

	err = showCommandHelp("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent command, got nil")
	}
}

func TestHandleSubcommands(t *testing.T) {
	var runCalled bool
	var runArgs []string

	commands["testcmd"] = Command{
		Name: "testcmd",
		Run: func(ctx context.Context, args []string) error {
			runCalled = true
			runArgs = args
			return nil
		},
	}

	ctx := context.Background()
	err := handleSubcommands(ctx, []string{"testcmd", "arg1", "arg2"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !runCalled {
		t.Fatal("expected run to be called")
	}
	if len(runArgs) != 2 || runArgs[0] != "arg1" || runArgs[1] != "arg2" {
		t.Errorf("expected [arg1 arg2], got %v", runArgs)
	}

	// Test default command (status)
	runCalled = false
	commands[defaultCommand] = Command{
		Name: defaultCommand,
		Run: func(ctx context.Context, args []string) error {
			runCalled = true
			return nil
		},
	}
	err = handleSubcommands(ctx, []string{})
	if err != nil {
		t.Fatalf("expected nil error for default command, got %v", err)
	}
	if !runCalled {
		t.Fatal("expected default command to run")
	}

	// Test unknown command
	err = handleSubcommands(ctx, []string{"unknowncmd"})
	if err == nil {
		t.Fatal("expected error for unknown command, got nil")
	}

	// Test help execution
	var buf bytes.Buffer
	oldLogger := logger
	logger = log.New(&buf, "", 0)
	err = handleSubcommands(ctx, []string{"testcmd", "--help"})
	logger = oldLogger
	if err != nil {
		t.Fatalf("expected nil error when showing help, got %v", err)
	}
}

func TestMainExec(t *testing.T) {
	t.Setenv(jettyStateDirEnv, filepath.Join(t.TempDir(), "state"))
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Test help
	os.Args = []string{"jetty", "--help"}
	main()

	// Test version
	os.Args = []string{"jetty", "--version"}
	main()

	// Test verbose
	os.Args = []string{"jetty", "--verbose", "status"}
	main()
}

func TestRegisteredCommands(t *testing.T) {
	t.Setenv(jettyStateDirEnv, filepath.Join(t.TempDir(), "state"))
	// Re-register to get original commands instead of mocked ones
	registerCommands()

	ctx := context.Background()

	// Test help command
	err := commands["help"].Run(ctx, []string{})
	if err != nil {
		t.Errorf("help command failed: %v", err)
	}
	err = commands["help"].Run(ctx, []string{"build"})
	if err != nil {
		t.Errorf("help build command failed: %v", err)
	}

	// Test version command
	err = commands["version"].Run(ctx, []string{})
	if err != nil {
		t.Errorf("version command failed: %v", err)
	}

	// Test clean command
	err = commands["clean"].Run(ctx, []string{})
	if err != nil {
		t.Errorf("clean command failed: %v", err)
	}
	// Test status command
	err = commands["status"].Run(ctx, []string{})
	if err != nil {
		t.Errorf("status command failed: %v", err)
	}

	// Test validate command (with a dummy file)
	dummyJetty := filepath.Join(t.TempDir(), "Jettyfile")
	os.WriteFile(dummyJetty, []byte("RUN echo hello\n"), 0644)
	err = commands["validate"].Run(ctx, []string{dummyJetty})
	if err != nil {
		t.Errorf("validate command failed: %v", err)
	}

	// Test build command
	err = commands["build"].Run(ctx, []string{dummyJetty})
	if err != nil {
		t.Errorf("build command failed: %v", err)
	}
	// Test help command with unknown
	err = commands["help"].Run(ctx, []string{"unknown-cmd-test"})
	if err == nil {
		t.Errorf("expected help to fail for unknown command")
	}

	// Test showCommandHelp with subcommands
	registerCommand("testsub", Command{
		Name:        "testsub",
		Description: "A test command with subcommands",
		Subcommands: map[string]*Command{
			"sub1": {Name: "sub1", Description: "Subcommand 1", Usage: "sub1"},
		},
	})
	err = showCommandHelp("testsub")
	if err != nil {
		t.Errorf("showCommandHelp failed for testsub: %v", err)
	}

	// Test parseFile missing file
	_, err = parseFile("nonexistent.txt")
	if err == nil {
		t.Errorf("expected parseFile to fail for missing file")
	}

	// Test handleSubcommands
	err = handleSubcommands(ctx, []string{"--help", "build"})
	if err != nil {
		t.Errorf("expected handleSubcommands help build to succeed")
	}

	err = handleSubcommands(ctx, []string{"--verbose", "version"})
	if err != nil {
		t.Errorf("expected handleSubcommands verbose version to succeed")
	}

	err = handleSubcommands(ctx, []string{"unknown-cmd"})
	if err == nil {
		t.Errorf("expected handleSubcommands to fail for unknown command")
	}

	err = handleSubcommands(ctx, []string{"build", "too", "many", "args", "here"})
	if err == nil {
		t.Errorf("expected handleSubcommands to fail for too many args")
	}
}
