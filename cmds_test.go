package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSortBuildInfos(t *testing.T) {
	now := time.Now()
	builds := []BuildInfo{
		{ID: "1", StartTime: now.Add(-time.Minute)},
		{ID: "2", StartTime: now},
		{ID: "3", StartTime: now.Add(time.Minute)},
	}
	sortBuildInfos(builds)
	if builds[0].ID != "3" || builds[1].ID != "2" || builds[2].ID != "1" {
		t.Errorf("sortBuildInfos failed: %v", builds)
	}
}

func TestFilterBuildInfos(t *testing.T) {
	builds := []BuildInfo{
		{ID: "1", Status: statusRunning, WorkerNode: "local", FileName: "Jettyfile"},
		{ID: "2", Status: statusCompleted, WorkerNode: "remote", FileName: "foo/Jettyfile"},
	}

	filtered := filterBuildInfos(builds, false, "")
	if len(filtered) != 1 || filtered[0].ID != "1" {
		t.Errorf("expected 1 running build, got %v", filtered)
	}

	filtered = filterBuildInfos(builds, true, "status=completed")
	if len(filtered) != 1 || filtered[0].ID != "2" {
		t.Errorf("expected 1 completed build, got %v", filtered)
	}

	filtered = filterBuildInfos(builds, true, "worker=remote")
	if len(filtered) != 1 || filtered[0].ID != "2" {
		t.Errorf("expected remote worker, got %v", filtered)
	}

	filtered = filterBuildInfos(builds, true, "file=foo")
	if len(filtered) != 1 || filtered[0].ID != "2" {
		t.Errorf("expected file foo, got %v", filtered)
	}

	filtered = filterBuildInfos(builds, true, "remote")
	if len(filtered) != 1 || filtered[0].ID != "2" {
		t.Errorf("expected match by string, got %v", filtered)
	}

	filtered = filterBuildInfos(builds, true, "badkey=value")
	if len(filtered) != 0 {
		t.Errorf("expected no match, got %v", filtered)
	}
}

func TestPrintBuildInfos(t *testing.T) {
	builds := []BuildInfo{
		{
			ID:         "123456789012345678901234567890",
			Status:     statusRunning,
			WorkerNode: "local",
			StartTime:  time.Now(),
			FileName:   "a/very/long/file/name/that/exceeds/the/limit/Jettyfile",
			Error:      "this is a very long error message that will definitely exceed the fifty character limit we impose",
		},
		{
			ID:         "2",
			Status:     statusCompleted,
			WorkerNode: "remote",
			StartTime:  time.Now().Add(-time.Hour),
			EndTime:    time.Now(),
			FileName:   "Jettyfile",
		},
	}
	printBuildInfos(builds)
}

func TestRegisteredCommandsEdges(t *testing.T) {
	registerCommands()

	// Test init with args
	err := commands["init"].Run(context.Background(), []string{"arg"})
	if err == nil {
		t.Error("expected init with args to fail")
	}

	// Test clean with args
	err = commands["clean"].Run(context.Background(), []string{"arg"})
	if err == nil {
		t.Error("expected clean with args to fail")
	}

	// Test validate nonexistent file
	err = commands["validate"].Run(context.Background(), []string{"nonexistent.txt"})
	if err == nil {
		t.Error("expected validate to fail for nonexistent file")
	}

	// Test validate parse error
	badFile := filepath.Join(t.TempDir(), "Jettyfile")
	os.WriteFile(badFile, []byte("BAD DIRECTIVE"), 0644)
	err = commands["validate"].Run(context.Background(), []string{badFile})
	if err == nil {
		t.Error("expected validate to fail for parse error")
	}
	// Test status command error cases
	statusCmd := commands["status"]
	err = statusCmd.Run(context.Background(), []string{"--invalid-flag"})
	if err == nil {
		t.Errorf("expected status to fail with invalid flag")
	}

	err = statusCmd.Run(context.Background(), []string{"positional"})
	if err == nil {
		t.Errorf("expected status to fail with positional arguments")
	}
}
