package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func executeInstructionConcurrent(inst Instruction, args map[string]string, resultChan chan<- string) error {
	inst.Directive = strings.TrimPrefix(inst.Directive, "*")
	return executeInstruction(inst, args, resultChan)
}

func executeInstruction(inst Instruction, args map[string]string, resultChan chan<- string) error {
	if len(inst.Directive) > 1 && !isAlphanumeric(inst.Directive[0]) {
		inst.Directive = inst.Directive[1:]
	}
	logMessage := func(format string, v ...interface{}) {
		msg := fmt.Sprintf(format, v...)
		resultChan <- msg + "\n"
	}
	type BoxInfo struct {
		Repository string
		Tag        string
	}
	var boxes map[string]BoxInfo
	if boxes == nil {
		boxes = make(map[string]BoxInfo)
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
	case "CPY", "*CPY":
		parts := strings.Fields(inst.Args)
		if len(parts) != 2 {
			return fmt.Errorf("CPY directive requires exactly two arguments: source and destination")
		}
		src := expandArgs(parts[0], args)
		dst := expandArgs(parts[1], args)
		copyFunc := func() {
			srcInfo, err := os.Stat(src)
			if err != nil {
				logMessage("Error accessing source: %v", err)
				return
			}
			if srcInfo.IsDir() {
				err = copyDir(src, dst)
			} else {
				err = copyFile(src, dst)
			}
			if err != nil {
				logMessage("Copy operation failed: %v", err)
			} else {
				logMessage("CPY: Copied from %s to %s", src, dst)
			}
		}
		if inst.Directive == "*CPY" {
			go copyFunc()
			logMessage("Started asynchronous copy: %s to %s", src, dst)
		} else {
			copyFunc()
		}
	case "SUB", "*SUB":
		referencedFile := inst.Args
		subBuildID := fmt.Sprintf("%s-sub-%d", args["BUILD_ID"], time.Now().UnixNano())
		subResultChan := make(chan string)
		subBuildInfoChan := make(chan BuildInfo)
		buildFunc := func() {
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
		}
		if inst.Directive == "*SUB" {
			go buildFunc()
			logMessage("Started asynchronous sub-build: %s", referencedFile)
		} else {
			buildFunc()
			logMessage("Completed synchronous sub-build: %s", referencedFile)
		}
	case "BOX":
		parts := strings.Fields(inst.Args)
		if len(parts) != 3 {
			return fmt.Errorf("BOX directive requires exactly three arguments: name, repository, and tag")
		}
		boxName, repository, tag := parts[0], parts[1], parts[2]
		boxes[boxName] = BoxInfo{Repository: repository, Tag: tag}
		logMessage("BOX: Created box %s with image %s:%s", boxName, repository, tag)

	case "USE":
		parts := strings.Fields(inst.Args)
		if len(parts) < 2 {
			return fmt.Errorf("USE directive requires at least two arguments: box name and command")
		}
		boxName, cmd := parts[0], strings.Join(parts[1:], " ")
		boxInfo, ok := boxes[boxName]
		if !ok {
			return fmt.Errorf("box not found: %s", boxName)
		}
		containerName := fmt.Sprintf("%s-%d", boxName, time.Now().UnixNano())
		containerID := ""
		err := execInContainer(Instruction{Args: cmd}, args, resultChan, &containerID, boxInfo.Repository, boxInfo.Tag, containerName)
		if err != nil {
			return fmt.Errorf("failed to execute in container: %v", err)
		}
		logMessage("USE: Executed command in box %s", boxName)

	case "FMT", "^FMT", "$FMT", "&FMT":
		parts := strings.SplitN(inst.Args, " ", 3)
		if len(parts) < 2 {
			return fmt.Errorf("%s directive requires at least two arguments: format string and arguments", inst.Directive)
		}
		formatString := parts[0]
		argsList := strings.Split(parts[1], " ")
		expandedArgs := make([]interface{}, len(argsList))
		for i, arg := range argsList {
			expandedArgs[i] = expandArgs(arg, args)
		}
		formattedString := fmt.Sprintf(formatString, expandedArgs...)
		switch inst.Directive {
		case "^FMT":
			file := expandArgs(argsList[len(argsList)-1], args)
			if err := appendToFile(file, formattedString); err != nil {
				return fmt.Errorf("failed to append to file: %v", err)
			}
			logMessage("^FMT: Appended formatted string to %s", file)
		case "$FMT":
			if len(parts) != 3 {
				return fmt.Errorf("$FMT directive requires three arguments: format string, arguments, and variable name")
			}
			varName := parts[2]
			if err := os.Setenv(varName, formattedString); err != nil {
				return fmt.Errorf("failed to set environment variable: %v", err)
			}
			logMessage("&FMT: Exported formatted string to environment variable %s", varName)
		case "&FMT":
			if len(parts) != 3 {
				return fmt.Errorf("&FMT directive requires three arguments: format string, arguments, and argument name")
			}
			argName := parts[2]
			args[argName] = formattedString
			logMessage("&FMT: Exported formatted string to argument %s", argName)
		default:
			logMessage("FMT: %s", formattedString)
		}
	case "JET":
		pluginName := strings.TrimSpace(inst.Args)
		pluginPath := filepath.Join("./plugins", pluginName)
		if _, err := os.Stat(pluginPath); os.IsNotExist(err) {
			return fmt.Errorf("plugin not found: %s", pluginName)
		}
		logMessage("JET: Found plugin %s", pluginName)
		// TODO: Implement plugin execution logic
	default:
		return fmt.Errorf("unknown directive: %s", inst.Directive)
	}
	return nil
}
