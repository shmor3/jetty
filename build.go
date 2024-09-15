package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/ory/dockertest/v3"
)

const (
	statusRunning   = "Running"
	statusCompleted = "Completed"
	statusFailed    = "Failed"
)

var (
	argsMutex        sync.Mutex
	envMutex         sync.Mutex
	workerPoolOnce   sync.Once
	globalWorkerPool []*WorkerNode
)

type BuildInfo struct {
	ID         string
	Status     string
	StartTime  time.Time
	EndTime    time.Time
	WorkerNode string
}

type WorkerNode struct {
	ID   string
	Jobs chan Job
	quit chan struct{}
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
		return fmt.Errorf("cmd execution failed: %v", err)
	}
	resultChan <- fmt.Sprintf("Done: %s\n", string(output))
	return nil
}

func execInContainer(inst Instruction, env map[string]string, resultChan chan<- string, containerID *string, repository string, tag string, containerName string) error {
	pool, err := dockertest.NewPool("")
	if err != nil {
		return fmt.Errorf("could not connect to docker: %v", err)
	}
	var resource *dockertest.Resource
	if *containerID == "" {
		resource, err = pool.RunWithOptions(&dockertest.RunOptions{
			Repository: repository,
			Tag:        tag,
			Name:       containerName,
			Cmd:        []string{"tail", "-f", "/dev/null"},
			Env:        formatEnv(env),
		})
		if err != nil {
			return fmt.Errorf("could not start resource: %v", err)
		}
		*containerID = resource.Container.ID
	} else {
		container, err := pool.Client.InspectContainer(*containerID)
		if err != nil {
			return fmt.Errorf("could not inspect container: %v", err)
		}
		resource = &dockertest.Resource{Container: container}
	}
	execCmd := []string{"/bin/sh", "-c", inst.Args}
	output, err := resource.Exec(execCmd, dockertest.ExecOptions{
		StdOut: os.Stdout,
		StdErr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("command execution failed: %v", err)
	}
	resultChan <- fmt.Sprintf("Done: %v\n", output)
	return nil
}
func formatEnv(env map[string]string) []string {
	var envSlice []string
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}
	return envSlice
}
