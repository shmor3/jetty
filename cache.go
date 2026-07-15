package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gofrs/flock"
)

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
	lockPath := cacheStorePath() + ".lock"
	stateDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache lock directory: %w", err)
	}
	_ = hideFile(stateDir)

	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
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
			return nil, fmt.Errorf("timeout waiting for lock on %s", lockPath)
		}
	}

	return func() {
		if err := fileLock.Unlock(); err != nil {
			logger.Printf("Warning: failed to unlock cache store: %v", err)
		}
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

	return os.Rename(tempPath, cachePath)
}

func hashFiles(workDir string, patterns []string) (string, error) {
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(workDir, pattern))
		if err != nil {
			return "", err
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				continue
			}
			if info.IsDir() {
				filepath.Walk(match, func(path string, walkInfo os.FileInfo, walkErr error) error {
					if walkErr == nil && !walkInfo.IsDir() {
						files = append(files, path)
					}
					return nil
				})
			} else {
				files = append(files, match)
			}
		}
	}

	if len(files) == 0 {
		return "empty", nil
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

		fmt.Fprintf(h, "%s:%d:%d:", f, info.Size(), info.ModTime().UnixNano())
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
		return false, nil
	}

	currentOutsHash, err := hashFiles(state.WorkDir, state.PendingOuts)
	if err != nil || currentOutsHash == "empty" || currentOutsHash != entry.Outputs["hash"] {
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
