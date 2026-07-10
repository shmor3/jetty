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
