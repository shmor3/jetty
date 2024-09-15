package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	statusRunning   = "Running"
	statusCompleted = "Completed"
	statusFailed    = "Failed"
)

var (
	argsMutex        sync.Mutex
	envMutex         sync.Mutex
	globalWorkerPool []*WorkerNode
	workerPoolOnce   sync.Once
)

type BuildInfo struct {
	ID         string
	Status     string
	StartTime  time.Time
	EndTime    time.Time
	WorkerNode string
}

type WorkerNode struct {
	ID    string
	Jobs  chan Job
	mutex sync.Mutex
	quit  chan struct{}
}

type Instruction struct {
	Directive string
	Args      string
}

type Job struct {
	BuildID       string
	FileName      string
	ResultChan    chan<- string
	BuildInfoChan chan<- BuildInfo
	WorkerNode    string
	Context       context.Context
}

func initializeGlobalWorkerPool(numWorkers int) {
	workerPoolOnce.Do(func() {
		globalWorkerPool = createWorkerPool(numWorkers)
	})
}
func createWorkerPool(numWorkers int) []*WorkerNode {
	workers := make([]*WorkerNode, numWorkers)
	for i := 0; i < numWorkers; i++ {
		workers[i] = NewWorkerNode(fmt.Sprintf("worker-%d", i+1))
		workers[i].Start()
	}
	return workers
}

func NewWorkerNode(id string) *WorkerNode {
	return &WorkerNode{
		ID:   id,
		Jobs: make(chan Job),
		quit: make(chan struct{}),
	}
}

func (w *WorkerNode) Start() {
	go func() {
		for {
			select {
			case job := <-w.Jobs:
				_ = job
			case <-w.quit:
				return
			}
		}
	}()
}

func (w *WorkerNode) Stop() {
	close(w.quit)
}

func assignBuildToWorker(job Job) {
	if job.Context == nil {
		job.ResultChan <- "Error: job context is nil"
		close(job.ResultChan)
		return
	}
	var selectedWorker *WorkerNode
	minJobs := int(^uint(0) >> 1)
	for _, worker := range globalWorkerPool {
		worker.mutex.Lock()
		jobCount := len(worker.Jobs)
		worker.mutex.Unlock()
		if jobCount < minJobs {
			selectedWorker = worker
			minJobs = jobCount
		}
	}
	if job.Context == nil {
		job.Context = context.Background()
	}
	select {
	case <-job.Context.Done():
		job.ResultChan <- "Job cancelled before assignment"
		close(job.ResultChan)
	case selectedWorker.Jobs <- job:
	}
}

func listActiveBuilds(buildInfoChan <-chan BuildInfo, outputChan chan<- map[string]BuildInfo, done <-chan struct{}) {
	activeBuilds := make(map[string]BuildInfo)
	var mutex sync.Mutex
	for {
		select {
		case buildInfo, ok := <-buildInfoChan:
			if !ok {
				return
			}
			mutex.Lock()
			switch buildInfo.Status {
			case statusRunning:
				activeBuilds[buildInfo.ID] = buildInfo
			case statusCompleted, statusFailed:
				delete(activeBuilds, buildInfo.ID)
			}
			outputChan <- activeBuilds
			mutex.Unlock()
		case <-done:
			return
		}
	}
}

func processBuild(job Job) {
	defer close(job.ResultChan)
	defer close(job.BuildInfoChan)
	if job.Context == nil {
		job.ResultChan <- "Error: job context is nil"
		return
	}
	buildInfo := BuildInfo{
		ID:         job.BuildID,
		Status:     statusRunning,
		StartTime:  time.Now(),
		WorkerNode: job.WorkerNode,
	}
	job.BuildInfoChan <- buildInfo
	select {
	case <-job.Context.Done():
		job.ResultChan <- "Build cancelled"
		buildInfo.Status = statusFailed
		job.BuildInfoChan <- buildInfo
		return
	default:
	}
	if job.FileName == "" {
		job.ResultChan <- "Error: please provide a file name"
		buildInfo.Status = statusFailed
		job.BuildInfoChan <- buildInfo
		return
	}
	instructions, err := parseFile(job.FileName)
	if err != nil {
		job.ResultChan <- fmt.Sprintf("Error parsing file: %v", err)
		buildInfo.Status = statusFailed
		job.BuildInfoChan <- buildInfo
		return
	}
	args := make(map[string]string)
	env := make(map[string]string)
	var wg sync.WaitGroup
	totalInstructions := len(instructions)
	currentInstruction := 0
	var cmdInstruction *Instruction
	var concurrentErrors []error
	for _, inst := range instructions {
		select {
		case <-job.Context.Done():
			job.ResultChan <- "Build cancelled"
			buildInfo.Status = statusFailed
			job.BuildInfoChan <- buildInfo
			return
		default:
			currentInstruction++
			if inst.Directive == "CMD" {
				if cmdInstruction != nil {
					job.ResultChan <- fmt.Sprintf("(%d/%d) Error: multiple CMD directives are not allowed", currentInstruction, totalInstructions)
					buildInfo.Status = statusFailed
					job.BuildInfoChan <- buildInfo
					return
				}
				cmdInstruction = &inst
				continue
			}
		}
		if strings.HasPrefix(inst.Directive, "*") {
			wg.Add(1)
			go func(instruction Instruction, count int) {
				defer wg.Done()
				err := executeInstructionConcurrent(instruction, args, job.ResultChan)
				if err != nil {
					job.ResultChan <- fmt.Sprintf("(%d/%d) Error: executing instruction: %v", count, totalInstructions, err)
					concurrentErrors = append(concurrentErrors, err)
				}
			}(inst, currentInstruction)
		} else {
			err := executeInstruction(inst, args, job.ResultChan)
			if err != nil {
				job.ResultChan <- fmt.Sprintf("(%d/%d) Error: executing instruction: %v", currentInstruction, totalInstructions, err)
				buildInfo.Status = statusFailed
				job.BuildInfoChan <- buildInfo
				return
			}
		}
	}
	wg.Wait()
	if len(concurrentErrors) > 0 {
		job.ResultChan <- fmt.Sprintf("Errors occurred during concurrent execution: %v", concurrentErrors)
		buildInfo.Status = statusFailed
		job.BuildInfoChan <- buildInfo
		return
	}
	if cmdInstruction != nil {
		err := executeCMD(*cmdInstruction, env, job.ResultChan)
		if err != nil {
			job.ResultChan <- fmt.Sprintf("(%d/%d) Error: executing CMD instruction: %v", totalInstructions, totalInstructions, err)
			buildInfo.Status = statusFailed
		} else {
			buildInfo.Status = statusCompleted
		}
	} else {
		buildInfo.Status = statusCompleted
	}
	job.BuildInfoChan <- buildInfo
}

func build(fileName string, buildID string, workerNode string, resultChan chan<- string, buildInfoChan chan<- BuildInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	done := make(chan struct{})
	job := Job{
		BuildID:       buildID,
		FileName:      fileName,
		ResultChan:    resultChan,
		BuildInfoChan: buildInfoChan,
		WorkerNode:    workerNode,
		Context:       ctx,
	}
	go func() {
		processBuild(job)
		close(done)
	}()
	select {
	case <-ctx.Done():
		resultChan <- "Build timed out or was cancelled"
		buildInfoChan <- BuildInfo{
			ID:         buildID,
			Status:     statusFailed,
			EndTime:    time.Now(),
			WorkerNode: workerNode,
		}
	case <-done:
	}
}

func executeCMD(inst Instruction, env map[string]string, resultChan chan<- string) error {
	cmd := exec.Command("sh", "-c", inst.Args)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("CMD execution failed: %v", err)
	}
	resultChan <- fmt.Sprintf("Done: %s\n", string(output))
	return nil
}

func executeInstructionConcurrent(inst Instruction, args map[string]string, resultChan chan<- string) error {
	inst.Directive = strings.TrimPrefix(inst.Directive, "*")
	return executeInstruction(inst, args, resultChan)
}

func isAlphanumeric(r byte) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func executeInstruction(inst Instruction, args map[string]string, resultChan chan<- string) error {
	if len(inst.Directive) > 1 && !isAlphanumeric(inst.Directive[0]) {
		inst.Directive = inst.Directive[1:]
	}
	logMessage := func(format string, v ...interface{}) {
		msg := fmt.Sprintf(format, v...)
		resultChan <- msg + "\n"
	}
	switch inst.Directive {
	case "ARG":
		parts := strings.SplitN(inst.Args, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ARG format: %s", inst.Args)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.Contains(key, " ") {
			return fmt.Errorf("only one ARG allowed per directive: %s", inst.Args)
		}
		argsMutex.Lock()
		args[key] = expandArgs(value, args)
		argsMutex.Unlock()
	case "ENV":
		parts := strings.SplitN(inst.Args, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ENV format: %s", inst.Args)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.Contains(key, " ") {
			return fmt.Errorf("only one ENV allowed per directive: %s", inst.Args)
		}
		expandedValue := expandArgs(value, args)
		envMutex.Lock()
		if err := os.Setenv(key, expandedValue); err != nil {
			envMutex.Unlock()
			return fmt.Errorf("failed to set environment variable: %v", err)
		}
		envMutex.Unlock()
		logMessage("ENV: %s=%s", key, expandedValue)
	case "RUN":
		expandedArgs := expandArgs(inst.Args, args)
		if err := validateLinuxCommand(expandedArgs); err != nil {
			return fmt.Errorf("invalid RUN command: %v", err)
		}
		cmd := exec.Command("sh", "-c", expandedArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command execution failed: %v", err)
		}
		logMessage("Done: %s", string(output))
	case "DIR":
		expandedArgs := expandArgs(inst.Args, args)
		err := os.MkdirAll(filepath.Clean(expandedArgs), 0755)
		if err != nil {
			return fmt.Errorf("directory creation failed: %v", err)
		}
		logMessage("DIR: %s", expandedArgs)
	case "WDR":
		parts := strings.Fields(inst.Args)
		if len(parts) != 1 {
			return fmt.Errorf("only one directory allowed per WDR directive: %s", inst.Args)
		}
		expandedDir := expandArgs(parts[0], args)
		expandedDir = filepath.Clean(expandedDir)
		if _, err := os.Stat(expandedDir); os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", expandedDir)
		}
		err := os.Chdir(expandedDir)
		if err != nil {
			return fmt.Errorf("failed to change directory: %v", err)
		}
		logMessage("WDR: Changed working directory to %s", expandedDir)
	case "FRM":
		referencedFile := inst.Args
		subBuildID := fmt.Sprintf("%s-sub-%d", args["BUILD_ID"], time.Now().UnixNano())
		subResultChan := make(chan string)
		subBuildInfoChan := make(chan BuildInfo)
		go build(referencedFile, subBuildID, args["WORKER_NODE"], subResultChan, subBuildInfoChan)
		timeout := time.After(5 * time.Minute)
		resultDone := make(chan bool)
		infoDone := make(chan bool)
		go func() {
			for result := range subResultChan {
				resultChan <- fmt.Sprintf("Sub-build %s: %s", subBuildID, result)
			}
			resultDone <- true
		}()
		go func() {
			for buildInfo := range subBuildInfoChan {
				if buildInfo.Status == statusCompleted || buildInfo.Status == statusFailed {
					resultChan <- fmt.Sprintf("Sub-build %s completed with status: %s", subBuildID, buildInfo.Status)
					infoDone <- true
					return
				}
			}
			infoDone <- true
		}()
		select {
		case <-resultDone:
			<-infoDone
		case <-infoDone:
			<-resultDone
		case <-timeout:
			resultChan <- fmt.Sprintf("Sub-build %s timed out", subBuildID)
		}
		logMessage("Done: Executed instructions from %s", referencedFile)
	default:
		return fmt.Errorf("unknown directive: %s", inst.Directive)
	}
	return nil
}
