package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

var cacheStoreMu sync.Mutex

// CacheEntry is a persisted DEP/OUT cache record keyed by a step's cache key.
type CacheEntry struct {
	Outputs map[string]string `json:"outputs"`
}

func cacheStorePath() string {
	stateDir := os.Getenv(jettyStateDirEnv)
	if stateDir == "" {
		stateDir = ".jetty"
	}
	return filepath.Join(stateDir, "cache.json")
}

func lockCacheStore() (func(), error) {
	// Block on the in-process mutex rather than spin-with-timeout: concurrent
	// async workers should wait their turn, not fail a correct build with a
	// spurious timeout. The cross-process flock below keeps its bounded wait.
	cacheStoreMu.Lock()

	lockPath := cacheStorePath() + ".lock"
	stateDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		cacheStoreMu.Unlock()
		return nil, fmt.Errorf("failed to create cache lock directory: %w", err)
	}
	_ = hideFile(stateDir)

	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
		cacheStoreMu.Unlock()
		return nil, fmt.Errorf("failed to check cache lock: %w", err)
	}

	if !locked {
		logger.Printf("Waiting for lock on %s...", lockPath)
		for i := 0; i < 50; i++ {
			locked, err = fileLock.TryLock()
			if err == nil && locked {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !locked {
			cacheStoreMu.Unlock()
			return nil, fmt.Errorf("timeout waiting for lock on %s", lockPath)
		}
	}

	return func() {
		if err := fileLock.Unlock(); err != nil {
			logger.Printf("Warning: failed to unlock cache store: %v", err)
		}
		cacheStoreMu.Unlock()
	}, nil
}

func readCacheLocked() (map[string]CacheEntry, error) {
	cachePath := cacheStorePath()
	data, err := os.ReadFile(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]CacheEntry), nil
		}
		return nil, err
	}
	var cache map[string]CacheEntry
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	if cache == nil {
		cache = make(map[string]CacheEntry)
	}
	return cache, nil
}

func writeCacheLocked(cache map[string]CacheEntry) error {
	cachePath := cacheStorePath()
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}

	tempPath := cachePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return err
	}
	defer os.Remove(tempPath)

	return os.Rename(tempPath, cachePath)
}

func hashFiles(workDir string, patterns []string) (string, error) {
	if len(patterns) == 0 {
		return "none", nil
	}

	var files []string
	for _, pattern := range patterns {
		globPattern := pattern
		if !filepath.IsAbs(globPattern) {
			globPattern = filepath.Join(workDir, pattern)
		}
		matches, err := filepath.Glob(globPattern)
		if err != nil {
			return "", err
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			if info.IsDir() {
				walkErr := filepath.Walk(match, func(path string, walkInfo os.FileInfo, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if !walkInfo.IsDir() {
						files = append(files, path)
					}
					return nil
				})
				if walkErr != nil {
					return "", walkErr
				}
			} else {
				files = append(files, match)
			}
		}
	}

	if len(files) == 0 {
		return "missing", nil
	}

	// Deduplicate and sort
	fileSet := make(map[string]struct{})
	for _, f := range files {
		fileSet[f] = struct{}{}
	}
	var uniqueFiles []string
	for f := range fileSet {
		uniqueFiles = append(uniqueFiles, f)
	}
	sort.Strings(uniqueFiles)

	h := sha256.New()
	for _, f := range uniqueFiles {
		info, err := os.Stat(f)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			continue
		}

		rel, err := filepath.Rel(workDir, f)
		if err != nil {
			// An absolute pattern can match a file that is not relatable to
			// workDir (e.g. a different Windows volume); hash its absolute path
			// rather than failing the build.
			rel = f
		}

		// Hash the relative path plus the file's actual contents so the cache
		// key reflects real inputs/outputs rather than just size and mtime
		// (which can collide on content changes and churn on mtime-only changes).
		fmt.Fprintf(h, "%s:%d:", filepath.ToSlash(rel), info.Size())
		file, err := os.Open(f)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, file); err != nil {
			file.Close()
			return "", err
		}
		file.Close()
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func checkCache(state *BuildState, inst Instruction) (bool, error) {
	if len(state.PendingDeps) == 0 && len(state.PendingOuts) == 0 {
		return false, nil
	}

	depsHash, err := hashFiles(state.WorkDir, state.PendingDeps)
	if err != nil {
		return false, err
	}

	keyHash := sha256.New()
	fmt.Fprintf(keyHash, "%s:%s:%s", inst.Directive, inst.Symbol, inst.Args)
	fmt.Fprintf(keyHash, ":%s", depsHash)

	var envKeys []string
	for k := range state.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		fmt.Fprintf(keyHash, ":%s=%s", k, state.Env[k])
	}

	// Fold in build ARGs so a change to an ARG referenced by the command
	// invalidates the cache. Exclude the runtime-injected identifiers, which
	// are unique per run and would otherwise defeat caching entirely.
	var argKeys []string
	for k := range state.Args {
		if k == "BUILD_ID" || k == "WORKER_NODE" {
			continue
		}
		argKeys = append(argKeys, k)
	}
	sort.Strings(argKeys)
	for _, k := range argKeys {
		fmt.Fprintf(keyHash, ":%s=%s", k, state.Args[k])
	}

	state.CurrentCacheKey = fmt.Sprintf("%x", keyHash.Sum(nil))

	unlock, err := lockCacheStore()
	if err != nil {
		return false, err
	}
	defer unlock()

	cache, err := readCacheLocked()
	if err != nil {
		return false, err
	}

	entry, ok := cache[state.CurrentCacheKey]
	if !ok {
		return false, nil
	}

	if len(state.PendingOuts) == 0 {
		if entry.Outputs["hash"] == "none" {
			return true, nil
		}
		return false, nil
	}

	currentOutsHash, err := hashFiles(state.WorkDir, state.PendingOuts)
	if err != nil {
		return false, nil
	}
	// Never hit the cache when the declared outputs are absent: the step must
	// run to (re)produce them.
	if currentOutsHash == "missing" {
		return false, nil
	}
	// Otherwise a hit requires the current outputs to match the recorded hash.
	if currentOutsHash != entry.Outputs["hash"] {
		return false, nil
	}

	return true, nil
}

func saveCache(state *BuildState) error {
	if state.CurrentCacheKey == "" {
		return nil
	}

	outsHash, err := hashFiles(state.WorkDir, state.PendingOuts)
	if err != nil {
		return err
	}

	unlock, err := lockCacheStore()
	if err != nil {
		return err
	}
	defer unlock()

	cache, err := readCacheLocked()
	if err != nil {
		return err
	}

	cache[state.CurrentCacheKey] = CacheEntry{
		Outputs: map[string]string{
			"hash": outsHash,
		},
	}

	return writeCacheLocked(cache)
}
