package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

const (
	statusRunning   = "Running"
	statusCompleted = "Completed"
	statusFailed    = "Failed"

	jettyStateDirEnv = "JETTY_STATE_DIR"
)

var (
	statusStoreMu  sync.Mutex
	ErrBuildFailed = errors.New("build failed")
	asyncSemaphore chan struct{}
)

func init() {
	asyncSemaphore = make(chan struct{}, runtime.NumCPU())
}

type BuildInfo struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time,omitempty"`
	WorkerNode string    `json:"worker_node"`
	FileName   string    `json:"file_name,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Instruction struct {
	Directive string
	Symbol    string
	Args      string
	Line      int
}

type Job struct {
	BuildID       string
	FileName      string
	ResultChan    chan<- string
	BuildInfoChan chan<- BuildInfo
	WorkerNode    string
	Context       context.Context
	InitialArgs   map[string]string
	InitialEnv    map[string]string
	EnvFile       string
	Depth         int
}

type BuildState struct {
	Context         context.Context
	FileName        string
	BaseDir         string
	WorkDir         string
	BuildID         string
	WorkerNode      string
	Args            map[string]string
	Env             map[string]string
	Boxes           map[string]BoxInfo
	DefaultBox      string
	ResultChan      chan<- string
	Cancel          context.CancelFunc
	Depth           int
	PendingDeps     []string
	PendingOuts     []string
	CurrentCacheKey string
}

type BoxInfo struct {
	Repository string
	Tag        string
}

func build(ctx context.Context, fileName string, buildID string, workerNode string, resultChan chan<- string, buildInfoChan chan<- BuildInfo, envFile string) error {
	return processBuild(Job{
		BuildID:       buildID,
		FileName:      fileName,
		ResultChan:    resultChan,
		BuildInfoChan: buildInfoChan,
		WorkerNode:    workerNode,
		Context:       ctx,
		EnvFile:       envFile,
	})
}

func processBuild(job Job) error {
	if job.ResultChan != nil {
		defer close(job.ResultChan)
	}
	if job.BuildInfoChan != nil {
		defer close(job.BuildInfoChan)
	}
	if job.Context == nil {
		job.Context = context.Background()
	}
	if job.BuildID == "" {
		job.BuildID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if job.WorkerNode == "" {
		job.WorkerNode = "local"
	}
	if job.FileName == "" {
		buildErr := errors.New("please provide a file name")
		sendResult(job.Context, job.ResultChan, "Error: "+buildErr.Error())
		return fmt.Errorf("%w: %w", ErrBuildFailed, buildErr)
	}
	if job.Depth > 50 {
		buildErr := errors.New("maximum sub-build depth exceeded")
		sendResult(job.Context, job.ResultChan, "Error: "+buildErr.Error())
		return fmt.Errorf("%w: %w", ErrBuildFailed, buildErr)
	}

	absFileName, err := filepath.Abs(job.FileName)
	if err != nil {
		return err
	}
	buildInfo := BuildInfo{
		ID:         job.BuildID,
		Status:     statusRunning,
		StartTime:  time.Now(),
		WorkerNode: job.WorkerNode,
		FileName:   absFileName,
	}
	publishBuildInfo(job.Context, job.BuildInfoChan, buildInfo)

	var buildErr error
	defer func() {
		r := recover()
		if r != nil {
			buildErr = fmt.Errorf("panic: %v", r)
		}
		buildInfo.EndTime = time.Now()
		if buildErr != nil {
			buildInfo.Status = statusFailed
			buildInfo.Error = buildErr.Error()
		} else {
			buildInfo.Status = statusCompleted
		}
		publishBuildInfo(job.Context, job.BuildInfoChan, buildInfo)
		if r != nil {
			panic(r)
		}
	}()

	instructions, err := parseFile(absFileName)
	if err != nil {
		buildErr = fmt.Errorf("parse %s: %w", job.FileName, err)
		sendResult(job.Context, job.ResultChan, "Error: "+buildErr.Error())
		return fmt.Errorf("%w: %w", ErrBuildFailed, buildErr)
	}

	execCtx, cancel := context.WithCancel(job.Context)
	defer cancel()
	state := &BuildState{
		Context:    execCtx,
		FileName:   absFileName,
		BaseDir:    filepath.Dir(absFileName),
		WorkDir:    filepath.Dir(absFileName),
		BuildID:    job.BuildID,
		WorkerNode: job.WorkerNode,
		Args:       cloneStringMap(job.InitialArgs),
		Env:        cloneStringMap(job.InitialEnv),
		Boxes:      make(map[string]BoxInfo),
		ResultChan: job.ResultChan,
		Cancel:     cancel,
		Depth:      job.Depth,
	}
	state.Args["BUILD_ID"] = job.BuildID
	state.Args["WORKER_NODE"] = job.WorkerNode

	if job.EnvFile != "" {
		loadEnvFile(state, job.EnvFile)
	} else {
		loadEnvFile(state, filepath.Join(state.BaseDir, ".env"))
	}

	if err := executeInstructions(state, instructions); err != nil {
		state.cancel()
		buildErr = err
		sendResult(job.Context, job.ResultChan, "Error: "+err.Error())
		return fmt.Errorf("%w: %w", ErrBuildFailed, buildErr)
	}
	return nil
}

func executeInstructions(state *BuildState, instructions []Instruction) error {
	var wg sync.WaitGroup
	errChan := make(chan error, len(instructions))
	var cmdInstruction *Instruction
	var syncErr error

	for i, inst := range instructions {
		if err := state.Context.Err(); err != nil {
			syncErr = err
			break
		}
		if inst.Directive == "CMD" {
			if cmdInstruction != nil {
				syncErr = fmt.Errorf("line %d: multiple CMD directives are not allowed", inst.Line)
				state.cancel()
				break
			}
			cmdCopy := inst
			cmdInstruction = &cmdCopy
			continue
		}

		count := i + 1
		if inst.Symbol == "*" {
			asyncState := state.snapshot()
			wg.Add(1)
			go func(instruction Instruction, instructionNumber int, instructionState *BuildState) {
				defer wg.Done()
				select {
				case asyncSemaphore <- struct{}{}:
					defer func() { <-asyncSemaphore }()
				case <-instructionState.Context.Done():
					errChan <- instructionState.Context.Err()
					return
				}
				if err := executeInstruction(instructionState, instruction); err != nil {
					errChan <- fmt.Errorf("(%d/%d) line %d [%s%s %s]: %w", instructionNumber, len(instructions), instruction.Line, instruction.Symbol, instruction.Directive, instruction.Args, err)
					instructionState.cancel()
				}
			}(inst, count, asyncState)
			continue
		}

		if err := executeInstruction(state, inst); err != nil {
			syncErr = fmt.Errorf("(%d/%d) line %d [%s%s %s]: %w", count, len(instructions), inst.Line, inst.Symbol, inst.Directive, inst.Args, err)
			state.cancel()
			break
		}
	}

	wg.Wait()
	close(errChan)
	var asyncErrors []error
	for err := range errChan {
		asyncErrors = append(asyncErrors, err)
	}
	if syncErr != nil {
		if len(asyncErrors) > 0 {
			return errors.Join(append([]error{syncErr}, asyncErrors...)...)
		}
		return syncErr
	}
	if len(asyncErrors) > 0 {
		return errors.Join(asyncErrors...)
	}

	if cmdInstruction != nil {
		if err := executeCMD(state, *cmdInstruction); err != nil {
			return fmt.Errorf("line %d [%s%s %s]: %w", cmdInstruction.Line, cmdInstruction.Symbol, cmdInstruction.Directive, cmdInstruction.Args, err)
		}
	}
	return nil
}

func sendResult(ctx context.Context, resultChan chan<- string, message string) {
	if resultChan == nil {
		return
	}
	select {
	case resultChan <- message:
	case <-ctx.Done():
	}
}

func publishBuildInfo(ctx context.Context, buildInfoChan chan<- BuildInfo, buildInfo BuildInfo) {
	if err := saveBuildInfo(buildInfo); err != nil {
		logger.Printf("Warning: failed to save build status: %v", err)
	}
	if buildInfoChan == nil {
		return
	}
	select {
	case buildInfoChan <- buildInfo:
	case <-ctx.Done():
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneBoxMap(source map[string]BoxInfo) map[string]BoxInfo {
	cloned := make(map[string]BoxInfo, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (state *BuildState) snapshot() *BuildState {
	return &BuildState{
		Context:    state.Context,
		FileName:   state.FileName,
		BaseDir:    state.BaseDir,
		WorkDir:    state.WorkDir,
		BuildID:    state.BuildID,
		WorkerNode: state.WorkerNode,
		Args:       cloneStringMap(state.Args),
		Env:        cloneStringMap(state.Env),
		Boxes:      cloneBoxMap(state.Boxes),
		DefaultBox: state.DefaultBox,
		ResultChan: state.ResultChan,
		Cancel:     state.Cancel,
		Depth:      state.Depth,
	}
}

func (state *BuildState) cancel() {
	if state.Cancel != nil {
		state.Cancel()
	}
}

func statusStorePath() string {
	stateDir := os.Getenv(jettyStateDirEnv)
	if stateDir == "" {
		stateDir = ".jetty"
	}
	return filepath.Join(stateDir, "builds.json")
}

func lockStatusStore() (func(), error) {
	lockPath := statusStorePath() + ".lock"
	stateDir := filepath.Dir(lockPath)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create lock directory %s: %w", stateDir, err)
	}
	_ = hideFile(stateDir)

	fileLock := flock.New(lockPath)
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("failed to check lock status: %w", err)
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
		fileLock.Unlock()
	}, nil
}

func saveBuildInfo(buildInfo BuildInfo) error {
	statusStoreMu.Lock()
	defer statusStoreMu.Unlock()

	unlock, err := lockStatusStore()
	if err != nil {
		return err
	}
	defer unlock()

	builds, err := readBuildInfosLocked()
	if err != nil {
		return err
	}
	replaced := false
	for i := range builds {
		if builds[i].ID == buildInfo.ID {
			builds[i] = buildInfo
			replaced = true
			break
		}
	}
	if !replaced {
		builds = append(builds, buildInfo)
	}
	if len(builds) > 1000 {
		builds = builds[len(builds)-1000:]
	}
	return writeBuildInfosLocked(builds)
}

func readBuildInfos() ([]BuildInfo, error) {
	statusStoreMu.Lock()
	defer statusStoreMu.Unlock()

	unlock, err := lockStatusStore()
	if err != nil {
		return nil, err
	}
	defer unlock()

	return readBuildInfosLocked()
}

func readBuildInfosLocked() ([]BuildInfo, error) {
	path := statusStorePath()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var builds []BuildInfo
	if err := json.Unmarshal(data, &builds); err != nil {
		return nil, err
	}
	return builds, nil
}

func writeBuildInfosLocked(builds []BuildInfo) error {
	path := statusStorePath()
	stateDir := filepath.Dir(path)
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	_ = hideFile(stateDir)
	data, err := json.MarshalIndent(builds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal builds: %w", err)
	}
	tmpFile, err := os.CreateTemp(stateDir, "builds-*.json.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write to temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func loadEnvFile(state *BuildState, filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
				v = v[1 : len(v)-1]
			}
			state.Env[k] = v
		}
	}
}
