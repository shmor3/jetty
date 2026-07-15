package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheLogic(t *testing.T) {
	tempDir := t.TempDir()
	os.Setenv(jettyStateDirEnv, tempDir)
	defer os.Unsetenv(jettyStateDirEnv)

	state := &BuildState{
		Context: context.Background(),
		WorkDir: tempDir,
		Env:     make(map[string]string),
		Args:    make(map[string]string),
	}

	// Create some dummy dependency files
	depFile := filepath.Join(tempDir, "dep.txt")
	os.WriteFile(depFile, []byte("dependency content"), 0644)

	outFile := filepath.Join(tempDir, "out.txt")

	// 1. Initial DEP and OUT
	instDep := Instruction{Directive: "DEP", Args: "dep.txt"}
	instOut := Instruction{Directive: "OUT", Args: "out.txt"}
	instRun := Instruction{Directive: "RUN", Args: "echo run"}

	_ = executeInstruction(state, instDep)
	_ = executeInstruction(state, instOut)

	cached, err := checkCache(state, instRun)
	if err != nil {
		t.Fatalf("unexpected error checking cache: %v", err)
	}
	if cached {
		t.Fatalf("expected cache miss, got hit")
	}

	// Simulate RUN generating the file
	os.WriteFile(outFile, []byte("output content"), 0644)
	err = saveCache(state)
	if err != nil {
		t.Fatalf("unexpected error saving cache: %v", err)
	}

	state.PendingDeps = nil
	state.PendingOuts = nil

	// 2. Second execution, should hit cache
	_ = executeInstruction(state, instDep)
	_ = executeInstruction(state, instOut)

	cached, err = checkCache(state, instRun)
	if err != nil {
		t.Fatalf("unexpected error checking cache: %v", err)
	}
	if !cached {
		t.Fatalf("expected cache hit, got miss")
	}

	// 3. Modify dependency, should miss cache
	os.WriteFile(depFile, []byte("changed dependency content"), 0644)

	cached, err = checkCache(state, instRun)
	if err != nil {
		t.Fatalf("unexpected error checking cache: %v", err)
	}
	if cached {
		t.Fatalf("expected cache miss after dep changed, got hit")
	}

	// Simulate RUN generating new output
	os.WriteFile(outFile, []byte("new output content"), 0644)
	err = saveCache(state)
	if err != nil {
		t.Fatalf("unexpected error saving cache: %v", err)
	}

	// 4. Modify output, should miss cache
	os.WriteFile(outFile, []byte("tampered output"), 0644)

	cached, err = checkCache(state, instRun)
	if err != nil {
		t.Fatalf("unexpected error checking cache: %v", err)
	}
	if cached {
		t.Fatalf("expected cache miss after output tampered, got hit")
	}

	// Test cache lock timeout
	unlock, _ := lockCacheStore()
	_, err = lockCacheStore()
	if err == nil {
		t.Fatalf("expected lock error on second attempt")
	}
	unlock()
}
