package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileDirectiveSymbols(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "Jettyfile")
	content := strings.Join([]string{
		"ARG NAME=world",
		"*RUN echo $NAME",
		"^FMT out.txt \"%s\" $NAME",
		"$FMT GREETING \"%s\" hello",
		"&FMT TARGET \"%s\" value",
		"",
	}, "\n")
	if err := os.WriteFile(fileName, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	instructions, err := parseFile(fileName)
	if err != nil {
		t.Fatalf("parseFile returned error: %v", err)
	}
	if len(instructions) != 5 {
		t.Fatalf("expected 5 instructions, got %d", len(instructions))
	}
	assertInstruction := func(index int, directive string, symbol string) {
		t.Helper()
		if instructions[index].Directive != directive || instructions[index].Symbol != symbol {
			t.Fatalf("instruction %d = %s/%s, want %s/%s",
				index,
				instructions[index].Directive,
				instructions[index].Symbol,
				directive,
				symbol,
			)
		}
	}
	assertInstruction(0, "ARG", "")
	assertInstruction(1, "RUN", "*")
	assertInstruction(2, "FMT", "^")
	assertInstruction(3, "FMT", "$")
	assertInstruction(4, "FMT", "&")
}

func TestParseFileRejectsUnsupportedDirectiveSymbols(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(fileName, []byte("*ARG NAME=value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parseFile(fileName)
	if err == nil {
		t.Fatal("expected parseFile to reject *ARG")
	}
	if !strings.Contains(err.Error(), "modifier * is not supported for directive ARG") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileRejectsFMTWithAsyncModifier(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "Jettyfile")
	if err := os.WriteFile(fileName, []byte("*FMT \"%s\" value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := parseFile(fileName)
	if err == nil {
		t.Fatal("expected parseFile to reject *FMT")
	}
	if !strings.Contains(err.Error(), "modifier * is not supported for directive FMT") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseFileMultilineInstruction(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "Jettyfile")
	content := "RUN echo one \\\n  echo two\n"
	if err := os.WriteFile(fileName, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	instructions, err := parseFile(fileName)
	if err != nil {
		t.Fatalf("parseFile returned error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected one instruction, got %d", len(instructions))
	}
	if !strings.Contains(instructions[0].Args, "\n") {
		t.Fatalf("expected multiline args to contain a newline, got %q", instructions[0].Args)
	}
	if instructions[0].Line != 1 {
		t.Fatalf("expected instruction to report starting line 1, got %d", instructions[0].Line)
	}
}

func TestParseFlags(t *testing.T) {
	// Backup and restore os.Args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	tests := []struct {
		name        string
		args        []string
		wantHelp    bool
		wantVersion bool
		wantVerbose bool
	}{
		{"no flags", []string{"jetty"}, false, false, false},
		{"help flag", []string{"jetty", "--help"}, true, false, false},
		{"short help flag", []string{"jetty", "-h"}, true, false, false},
		{"version flag", []string{"jetty", "--version"}, false, true, false},
		{"verbose flag", []string{"jetty", "--verbose"}, false, false, true},
		{"short verbose flag", []string{"jetty", "-v"}, false, false, true},
		{"invalid flag", []string{"jetty", "--invalid"}, false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			config := parseFlags()
			if config.Help != tc.wantHelp {
				t.Errorf("parseFlags() Help = %v, want %v", config.Help, tc.wantHelp)
			}
			if config.Version != tc.wantVersion {
				t.Errorf("parseFlags() Version = %v, want %v", config.Version, tc.wantVersion)
			}
			if config.Verbose != tc.wantVerbose {
				t.Errorf("parseFlags() Verbose = %v, want %v", config.Verbose, tc.wantVerbose)
			}
		})
	}
}

func TestValidateArgs(t *testing.T) {
	cmd := Command{
		MinArgs: 1,
		MaxArgs: 2,
	}

	if err := validateArgs(cmd, []string{"arg1"}); err != nil {
		t.Errorf("expected nil error for valid args, got %v", err)
	}

	if err := validateArgs(cmd, []string{"arg1", "arg2"}); err != nil {
		t.Errorf("expected nil error for valid args, got %v", err)
	}

	if err := validateArgs(cmd, []string{}); err == nil {
		t.Error("expected error for too few args, got nil")
	}

	if err := validateArgs(cmd, []string{"arg1", "arg2", "arg3"}); err == nil {
		t.Error("expected error for too many args, got nil")
	}
}

func TestParseFileErrors(t *testing.T) {
	dir := t.TempDir()

	// Test file open error
	_, err := parseFile(filepath.Join(dir, "nonexistent"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}

	// Test invalid instruction (no args)
	badJetty := filepath.Join(dir, "JettyfileBad")
	os.WriteFile(badJetty, []byte("RUN\n"), 0644)
	_, err = parseFile(badJetty)
	if err == nil || !strings.Contains(err.Error(), "invalid instruction") {
		t.Errorf("expected invalid instruction error, got %v", err)
	}

	// Test unterminated multi-line
	multiJetty := filepath.Join(dir, "JettyfileMulti")
	os.WriteFile(multiJetty, []byte("RUN echo \\\n"), 0644)
	_, err = parseFile(multiJetty)
	if err == nil || !strings.Contains(err.Error(), "unterminated multi-line") {
		t.Errorf("expected unterminated multi-line error, got %v", err)
	}
}

func TestParseDirectiveTokenEdgeCases(t *testing.T) {
	// Empty token
	_, _, err := parseDirectiveToken("")
	if err == nil || !strings.Contains(err.Error(), "empty directive") {
		t.Errorf("expected empty directive error, got %v", err)
	}

	// Just modifier
	_, _, err = parseDirectiveToken("*")
	if err == nil || !strings.Contains(err.Error(), "invalid directive") {
		t.Errorf("expected invalid directive error, got %v", err)
	}

	// Missing modifier logic
	originalSymbols := directiveSymbols["BOX"]
	directiveSymbols["BOX"] = map[string]bool{"*": true} // ONLY allows *

	_, _, err = parseDirectiveToken("BOX")
	if err == nil || !strings.Contains(err.Error(), "requires a supported modifier") {
		t.Errorf("expected missing modifier error, got %v", err)
	}

	_, _, err = parseDirectiveToken("^BOX")
	if err == nil || !strings.Contains(err.Error(), "is not supported for directive") {
		t.Errorf("expected unsupported modifier error, got %v", err)
	}

	directiveSymbols["BOX"] = originalSymbols
}

func TestParseFileEmptyAndComments(t *testing.T) {
	dir := t.TempDir()
	fileName := filepath.Join(dir, "Jettyfile")
	content := "\n# comment\nRUN echo hello\n\n# another comment\n"
	os.WriteFile(fileName, []byte(content), 0644)

	instructions, err := parseFile(fileName)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(instructions) != 1 {
		t.Errorf("expected 1 instruction, got %d", len(instructions))
	}
}

func TestParseFileDirectory(t *testing.T) {
	dir := t.TempDir()
	// parseFile on a directory should fail either at os.Open or scanner.Err
	_, err := parseFile(dir)
	if err == nil {
		t.Error("expected error when parsing a directory")
	}
}
