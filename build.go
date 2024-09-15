package main

import (
	"fmt"
	"os"
	"os/exec"
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
	argsMutex sync.Mutex
	envMutex  sync.Mutex
)

type BuildInfo struct {
	ID         string
	Status     string
	StartTime  time.Time
	EndTime    time.Time
	WorkerNode string
}

type Instruction struct {
	Directive string
	Args      string
}

type WorkerNode struct {
	ID    string
	Jobs  chan Job
	mutex sync.Mutex
}

type Job struct {
	BuildID       string
	FileName      string
	ResultChan    chan<- string
	BuildInfoChan chan<- BuildInfo
}

func NewWorkerNode(id string) *WorkerNode {
	return &WorkerNode{
		ID:   id,
		Jobs: make(chan Job),
	}
}

func (w *WorkerNode) Start() {
	go func() {
		for job := range w.Jobs {
			build(job.FileName, job.BuildID, w.ID, job.ResultChan, job.BuildInfoChan)
		}
	}()
}

func createWorkerPool(numWorkers int) []*WorkerNode {
	workers := make([]*WorkerNode, numWorkers)
	for i := 0; i < numWorkers; i++ {
		workers[i] = NewWorkerNode(fmt.Sprintf("worker-%d", i+1))
		workers[i].Start()
	}
	return workers
}

func assignBuildToWorker(workers []*WorkerNode, job Job) {
	var selectedWorker *WorkerNode
	minJobs := int(^uint(0) >> 1) // Max int value
	for _, worker := range workers {
		worker.mutex.Lock()
		jobCount := len(worker.Jobs)
		worker.mutex.Unlock()
		if jobCount < minJobs {
			selectedWorker = worker
			minJobs = jobCount
		}
	}
	if selectedWorker != nil {
		selectedWorker.mutex.Lock()
		selectedWorker.Jobs <- job
		selectedWorker.mutex.Unlock()
	}
}

func listActiveBuilds(buildInfoChan <-chan BuildInfo, outputChan chan<- map[string]BuildInfo) {
	activeBuilds := make(map[string]BuildInfo)
	var mutex sync.Mutex
	for buildInfo := range buildInfoChan {
		mutex.Lock()
		switch buildInfo.Status {
		case statusRunning:
			activeBuilds[buildInfo.ID] = buildInfo
		case statusCompleted, statusFailed:
			delete(activeBuilds, buildInfo.ID)
		}
		outputChan <- activeBuilds
		mutex.Unlock()
	}
}

func build(fileName string, buildID string, workerNode string, resultChan chan<- string, buildInfoChan chan<- BuildInfo) {
	numWorkers := 4
	workers := createWorkerPool(numWorkers)
	job := Job{
		BuildID:       buildID,
		FileName:      fileName,
		ResultChan:    resultChan,
		BuildInfoChan: buildInfoChan,
	}
	assignBuildToWorker(workers, job)
	buildInfo := BuildInfo{
		ID:         buildID,
		Status:     "Running",
		StartTime:  time.Now(),
		WorkerNode: workerNode,
	}
	buildInfoChan <- buildInfo
	go func() {
		defer close(resultChan)
		defer func() {
			buildInfo.EndTime = time.Now()
			buildInfo.Status = "Completed"
			buildInfoChan <- buildInfo
		}()
		if fileName == "" {
			resultChan <- "Error: please provide a file name"
			buildInfo.Status = "Failed"
			buildInfoChan <- buildInfo
			return
		}
		instructions, err := parseFile(fileName)
		if err != nil {
			resultChan <- fmt.Sprintf("Error parsing file: %v", err)
			buildInfo.Status = "Failed"
			buildInfoChan <- buildInfo
			return
		}
		args := make(map[string]string)
		env := make(map[string]string)
		var cmdInstruction *Instruction
		var wg sync.WaitGroup
		for _, inst := range instructions {
			if inst.Directive == "CMD" {
				if cmdInstruction != nil {
					resultChan <- "Error: multiple CMD directives are not allowed"
					buildInfo.Status = "Failed"
					buildInfoChan <- buildInfo
					return
				}
				cmdInstruction = &inst
				continue
			}
			if strings.HasPrefix(inst.Directive, "*") {
				wg.Add(1)
				go func(instruction Instruction) {
					defer wg.Done()
					err := executeInstructionConcurrent(instruction, args, resultChan)
					if err != nil {
						resultChan <- fmt.Sprintf("Error executing instruction: %v", err)
					}
				}(inst)
			} else {
				err := executeInstruction(inst, args, resultChan)
				if err != nil {
					resultChan <- fmt.Sprintf("Error executing instruction: %v", err)
					buildInfo.Status = "Failed"
					buildInfoChan <- buildInfo
					return
				}
			}
		}
		wg.Wait()
		if cmdInstruction != nil {
			err := executeCMD(*cmdInstruction, env, resultChan)
			if err != nil {
				resultChan <- fmt.Sprintf("Error executing CMD instruction: %v", err)
				buildInfo.Status = "Failed"
				buildInfoChan <- buildInfo
			}
		}
	}()
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

func executeInstruction(inst Instruction, args map[string]string, resultChan chan<- string) error {
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
		logMessage("ENV: %s=%s\n", key, expandedValue)
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
		logMessage("Done: %s\n", string(output))
	case "DIR":
		expandedArgs := expandArgs(inst.Args, args)
		err := os.MkdirAll(expandedArgs, 0755)
		if err != nil {
			return fmt.Errorf("directory creation failed: %v", err)
		}
		logMessage("Done: %s\n", "directory created")
	case "WDR":
		parts := strings.Fields(inst.Args)
		if len(parts) != 1 {
			return fmt.Errorf("only one directory allowed per WDR directive: %s", inst.Args)
		}
		expandedDir := expandArgs(parts[0], args)
		if _, err := os.Stat(expandedDir); os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", expandedDir)
		}
		err := os.Chdir(expandedDir)
		if err != nil {
			return fmt.Errorf("failed to change directory: %v", err)
		}
		logMessage("WDR: Changed working directory to %s", expandedDir)
	default:
		return fmt.Errorf("unknown directive: %s", inst.Directive)
	}
	return nil
}
