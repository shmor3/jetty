package main

import (
	"context"
	"path/filepath"
	"testing"
)

// --- Benchmarks ---

func BenchmarkSplitArgs(b *testing.B) {
	input := `this is a "test string" with 'some quotes' and \escaped chars`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = splitArgs(input)
	}
}

func BenchmarkExpand(b *testing.B) {
	state := &BuildState{
		Env:  map[string]string{"ENV_VAR": "env_value"},
		Args: map[string]string{"ARG_VAR": "arg_value"},
	}
	input := `Testing expand: $ENV_VAR and &ARG_VAR and ${MISSING} and &{MISSING}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.expand(input)
	}
}

func BenchmarkParseAssignment(b *testing.B) {
	input := "MY_VAR = some_value"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = parseAssignment(input, "ENV")
	}
}

func BenchmarkParseImageReference(b *testing.B) {
	input := "ubuntu:20.04"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseImageReference(input)
	}
}

func BenchmarkIsSubpath(b *testing.B) {
	parent := filepath.Join("a", "b", "c")
	sub := filepath.Join("a", "b", "c", "d", "e")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isSubpath(parent, sub)
	}
}

func BenchmarkMatchesBuildFilter(b *testing.B) {
	build := BuildInfo{
		ID:         "123",
		Status:     statusCompleted,
		WorkerNode: "local",
		FileName:   "Jettyfile",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = matchesBuildFilter(build, "status=Completed")
		_ = matchesBuildFilter(build, "worker=remote")
	}
}

// --- Fuzzing ---

func FuzzSplitArgs(f *testing.F) {
	f.Add(`simple string`)
	f.Add(`"quoted string"`)
	f.Add(`'single quotes'`)
	f.Add(`mixed "quotes 'and' stuff"`)
	f.Add(`escaped \" quotes`)
	f.Add(``)

	f.Fuzz(func(t *testing.T, orig string) {
		// Should not panic on any input
		_, _ = splitArgs(orig)
	})
}

func FuzzExpand(f *testing.F) {
	f.Add(`$ENV_VAR`)
	f.Add(`&ARG_VAR`)
	f.Add(`${ENV_VAR}`)
	f.Add(`&{ARG_VAR}`)
	f.Add(`$$`)
	f.Add(`&&`)
	f.Add(`just a string`)

	state := &BuildState{
		Env:  map[string]string{"ENV_VAR": "1", "OTHER": "2"},
		Args: map[string]string{"ARG_VAR": "3", "OTHER_ARG": "4"},
	}

	f.Fuzz(func(t *testing.T, orig string) {
		// Should not panic on any input
		_ = state.expand(orig)
	})
}

func FuzzParseAssignment(f *testing.F) {
	f.Add(`KEY=value`)
	f.Add(`KEY = value`)
	f.Add(`KEY=`)
	f.Add(`=value`)
	f.Add(`NO_EQUALS`)
	f.Add(`KEY=val=ue`)
	f.Add(`123KEY=value`)

	f.Fuzz(func(t *testing.T, orig string) {
		// Should not panic on any input
		_, _, _ = parseAssignment(orig, "TEST")
	})
}

func FuzzParseImageReference(f *testing.F) {
	f.Add(`ubuntu`)
	f.Add(`ubuntu:latest`)
	f.Add(`repo/image:tag`)
	f.Add(`repo/image`)
	f.Add(`:tag`)
	f.Add(`repo/image:tag:extra`)
	f.Add(`registry:5000/repo/image:tag`)

	f.Fuzz(func(t *testing.T, orig string) {
		// Should not panic on any input
		_, _ = parseImageReference(orig)
	})
}

func FuzzExecuteFormat(f *testing.F) {
	f.Add(`^`, `file.txt %s text`)
	f.Add(`$`, `VAR %s text`)
	f.Add(`&`, `VAR %s text`)
	f.Add(``, `format string`)
	f.Add(`invalid`, `format string`)

	f.Fuzz(func(t *testing.T, symbol string, args string) {
		state := &BuildState{
			Context: context.Background(),
			Env:     make(map[string]string),
			Args:    make(map[string]string),
			WorkDir: t.TempDir(),
		}
		// Should not panic
		_ = executeFormat(state, Instruction{
			Directive: "FMT",
			Symbol:    symbol,
			Args:      args,
		})
	})
}
