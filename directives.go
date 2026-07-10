package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/shlex"
	"github.com/ory/dockertest/v3"
)

func executeInstruction(state *BuildState, inst Instruction) error {
	switch inst.Directive {
	case "ARG":
		key, value, err := parseAssignment(inst.Args, "ARG")
		if err != nil {
			return err
		}
		state.Args[key] = state.expand(value)
	case "ENV":
		key, value, err := parseAssignment(inst.Args, "ENV")
		if err != nil {
			return err
		}
		state.Env[key] = state.expand(value)
		state.log("ENV: %s=%s", key, state.Env[key])
	case "RUN":
		if err := executeShell(state, "RUN", inst.Args); err != nil {
			return err
		}
	case "DIR":
		dir, err := state.singlePath(inst.Args, "DIR")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("directory creation failed: %w", err)
		}
		state.log("DIR: %s", dir)
	case "WDR":
		dir, err := state.singlePath(inst.Args, "WDR")
		if err != nil {
			return err
		}
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("working directory does not exist: %s", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("working directory is not a directory: %s", dir)
		}
		state.WorkDir = dir
		state.log("WDR: %s", dir)
	case "CPY":
		if err := executeCopy(state, inst.Args); err != nil {
			return err
		}
	case "SUB":
		if err := executeSubBuild(state, inst.Args); err != nil {
			return err
		}
	case "FRM":
		box, err := parseImageReference(strings.TrimSpace(state.expand(inst.Args)))
		if err != nil {
			return err
		}
		state.Boxes["default"] = box
		state.DefaultBox = "default"
		state.log("FRM: default box %s:%s", box.Repository, box.Tag)
	case "BOX":
		if err := executeBox(state, inst.Args); err != nil {
			return err
		}
	case "USE":
		if err := executeUse(state, inst.Args); err != nil {
			return err
		}
	case "FMT":
		if err := executeFormat(state, inst); err != nil {
			return err
		}
	case "JET":
		if err := executePlugin(state, inst.Args); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown directive: %s", inst.Directive)
	}
	return nil
}

func executeCMD(state *BuildState, inst Instruction) error {
	return executeShell(state, "CMD", inst.Args)
}

func executeShell(state *BuildState, label string, script string) error {
	expandedScript := strings.TrimSpace(state.expand(script))
	if err := validateLinuxCommand(expandedScript); err != nil {
		return fmt.Errorf("invalid %s command: %w", label, err)
	}
	cmd := shellCommand(state.Context, expandedScript)
	cmd.Dir = state.WorkDir
	cmd.Env = state.commandEnv()
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		state.log("%s: %s", label, strings.TrimRight(string(output), "\r\n"))
	}
	if err != nil {
		return fmt.Errorf("%s command failed: %w", label, err)
	}
	return nil
}

func executeCopy(state *BuildState, args string) error {
	parts, err := splitArgs(args)
	if err != nil {
		return err
	}
	if len(parts) != 2 {
		return fmt.Errorf("CPY requires exactly two arguments: source and destination")
	}
	src := state.resolvePath(state.expand(parts[0]))
	dst := state.resolvePath(state.expand(parts[1]))
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("source does not exist: %s", src)
	}
	if srcInfo.IsDir() {
		if isSubpath(src, dst) {
			return fmt.Errorf("cannot copy directory %s into itself at %s", src, dst)
		}
		err = copyDir(src, dst)
	} else {
		err = copyFile(src, dst)
	}
	if err != nil {
		return fmt.Errorf("copy failed: %w", err)
	}
	state.log("CPY: %s -> %s", src, dst)
	return nil
}

func executeSubBuild(state *BuildState, args string) error {
	referencedFile, err := state.singlePath(args, "SUB")
	if err != nil {
		return err
	}
	subBuildID := fmt.Sprintf("%s-sub-%d", state.BuildID, time.Now().UnixNano())
	subResultChan := make(chan string)
	subBuildInfoChan := make(chan BuildInfo)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for result := range subResultChan {
			state.log("Sub-build %s: %s", subBuildID, strings.TrimRight(result, "\r\n"))
		}
	}()
	go func() {
		defer wg.Done()
		for buildInfo := range subBuildInfoChan {
			if buildInfo.Status == statusCompleted || buildInfo.Status == statusFailed {
				state.log("Sub-build %s status: %s", subBuildID, buildInfo.Status)
			}
		}
	}()

	err = processBuild(Job{
		BuildID:       subBuildID,
		FileName:      referencedFile,
		ResultChan:    subResultChan,
		BuildInfoChan: subBuildInfoChan,
		WorkerNode:    state.WorkerNode,
		Context:       state.Context,
		InitialArgs:   state.Args,
		InitialEnv:    state.Env,
	})
	wg.Wait()
	if err != nil {
		return fmt.Errorf("sub-build %s failed: %w", referencedFile, err)
	}
	state.log("SUB: %s", referencedFile)
	return nil
}

func executeBox(state *BuildState, args string) error {
	parts, err := splitArgs(args)
	if err != nil {
		return err
	}
	if len(parts) != 2 && len(parts) != 3 {
		return fmt.Errorf("BOX requires name and image, or name, repository, and tag")
	}
	name := state.expand(parts[0])
	var box BoxInfo
	if len(parts) == 2 {
		box, err = parseImageReference(state.expand(parts[1]))
	} else {
		box = BoxInfo{
			Repository: state.expand(parts[1]),
			Tag:        state.expand(parts[2]),
		}
	}
	if err != nil {
		return err
	}
	state.Boxes[name] = box
	if state.DefaultBox == "" {
		state.DefaultBox = name
	}
	state.log("BOX: %s=%s:%s", name, box.Repository, box.Tag)
	return nil
}

func executeUse(state *BuildState, args string) error {
	parts, err := splitArgs(args)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("USE requires a box name and command, or a default FRM box and command")
	}

	boxName := state.DefaultBox
	command := strings.TrimSpace(args)
	candidateBoxName := state.expand(parts[0])
	if _, ok := state.Boxes[candidateBoxName]; ok {
		boxName = candidateBoxName
		command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), parts[0]))
	}
	if boxName == "" {
		return fmt.Errorf("USE requires a known box name when no FRM default is configured")
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("USE requires a command")
	}
	box := state.Boxes[boxName]
	output, err := execInContainer(state.Context, command, state.Env, box)
	if len(output) > 0 {
		state.log("USE %s: %s", boxName, strings.TrimRight(output, "\r\n"))
	}
	if err != nil {
		return err
	}
	return nil
}

func executeFormat(state *BuildState, inst Instruction) error {
	parts, err := splitArgs(inst.Args)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("FMT requires a format string")
	}

	switch inst.Symbol {
	case "":
		formatted := sprintfExpanded(state, parts[0], parts[1:])
		state.log("FMT: %s", formatted)
	case "^":
		if len(parts) < 2 {
			return fmt.Errorf("^FMT requires a file and format string")
		}
		file := state.resolvePath(state.expand(parts[0]))
		formatted := sprintfExpanded(state, parts[1], parts[2:])
		if err := appendToFile(file, formatted); err != nil {
			return fmt.Errorf("failed to append to file: %w", err)
		}
		state.log("^FMT: %s", file)
	case "$":
		if len(parts) < 2 {
			return fmt.Errorf("$FMT requires an environment variable and format string")
		}
		name := state.expand(parts[0])
		if !isValidName(name) {
			return fmt.Errorf("invalid environment variable name: %s", name)
		}
		state.Env[name] = sprintfExpanded(state, parts[1], parts[2:])
		state.log("$FMT: %s=%s", name, state.Env[name])
	case "&":
		if len(parts) < 2 {
			return fmt.Errorf("&FMT requires an argument name and format string")
		}
		name := state.expand(parts[0])
		if !isValidName(name) {
			return fmt.Errorf("invalid argument name: %s", name)
		}
		state.Args[name] = sprintfExpanded(state, parts[1], parts[2:])
		state.log("&FMT: %s=%s", name, state.Args[name])
	default:
		return fmt.Errorf("unsupported FMT modifier: %s", inst.Symbol)
	}
	return nil
}

func executePlugin(state *BuildState, args string) error {
	parts, err := splitArgs(args)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("JET requires a plugin name")
	}
	pluginName := state.expand(parts[0])
	pluginPath := pluginName
	if !filepath.IsAbs(pluginPath) && filepath.Base(pluginPath) == pluginPath {
		pluginPath = filepath.Join("plugins", pluginPath)
	}
	pluginPath = state.resolvePath(pluginPath)
	pluginArgs := make([]string, 0, len(parts)-1)
	for _, arg := range parts[1:] {
		pluginArgs = append(pluginArgs, state.expand(arg))
	}
	cmd := exec.CommandContext(state.Context, pluginPath, pluginArgs...)
	cmd.Dir = state.WorkDir
	cmd.Env = state.commandEnv()
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		state.log("JET %s: %s", pluginName, strings.TrimRight(string(output), "\r\n"))
	}
	if err != nil {
		return fmt.Errorf("plugin %s failed: %w", pluginName, err)
	}
	return nil
}

func parseAssignment(args string, directive string) (string, string, error) {
	parts := strings.SplitN(args, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid %s format, expected KEY=value", directive)
	}
	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])
	if !isValidName(key) {
		return "", "", fmt.Errorf("invalid %s key: %s", directive, key)
	}
	return key, value, nil
}

var validNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isValidName(name string) bool {
	return validNamePattern.MatchString(name)
}

func splitArgs(args string) ([]string, error) {
	parts, err := shlex.Split(args)
	if err != nil {
		return nil, fmt.Errorf("invalid quoted arguments: %w", err)
	}
	return parts, nil
}

func (state *BuildState) singlePath(args string, directive string) (string, error) {
	parts, err := splitArgs(args)
	if err != nil {
		return "", err
	}
	if len(parts) != 1 {
		return "", fmt.Errorf("%s requires exactly one path argument", directive)
	}
	return state.resolvePath(state.expand(parts[0])), nil
}

func isSubpath(parent string, child string) bool {
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	parentAbs = filepath.Clean(parentAbs)
	childAbs = filepath.Clean(childAbs)
	if parentAbs == childAbs {
		return true
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (state *BuildState) resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(state.WorkDir, path))
}

func (state *BuildState) commandEnv() []string {
	env := os.Environ()
	for key, value := range state.Env {
		env = append(env, fmt.Sprintf("%s=%s", key, value))
	}
	return env
}

func (state *BuildState) log(format string, v ...interface{}) {
	sendResult(state.Context, state.ResultChan, fmt.Sprintf(format, v...))
}

func (state *BuildState) expand(value string) string {
	return os.Expand(value, func(key string) string {
		if arg, ok := state.Args[key]; ok {
			return arg
		}
		if env, ok := state.Env[key]; ok {
			return env
		}
		return "$" + key
	})
}

func shellCommand(ctx context.Context, script string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if shell, err := exec.LookPath("sh"); err == nil {
			return exec.CommandContext(ctx, shell, "-c", script)
		}
		return exec.CommandContext(ctx, "cmd", "/C", script)
	}
	return exec.CommandContext(ctx, "sh", "-c", script)
}

func sprintfExpanded(state *BuildState, format string, values []string) string {
	expanded := make([]interface{}, len(values))
	for i, value := range values {
		expanded[i] = state.expand(value)
	}
	return fmt.Sprintf(format, expanded...)
}

func parseImageReference(image string) (BoxInfo, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return BoxInfo{}, fmt.Errorf("image reference is required")
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return BoxInfo{Repository: image[:lastColon], Tag: image[lastColon+1:]}, nil
	}
	return BoxInfo{Repository: image, Tag: "latest"}, nil
}

func execInContainer(ctx context.Context, command string, env map[string]string, box BoxInfo) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	pool, err := dockertest.NewPool("")
	if err != nil {
		return "", fmt.Errorf("could not connect to docker: %w", err)
	}
	resource, err := pool.RunWithOptions(&dockertest.RunOptions{
		Repository: box.Repository,
		Tag:        box.Tag,
		Name:       fmt.Sprintf("jetty-%d", time.Now().UnixNano()),
		Cmd:        []string{"tail", "-f", "/dev/null"},
		Env:        formatEnv(env),
	})
	if err != nil {
		return "", fmt.Errorf("could not start container %s:%s: %w", box.Repository, box.Tag, err)
	}
	defer func() {
		if err := pool.Purge(resource); err != nil {
			logger.Printf("Warning: failed to purge container: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode, err := resource.Exec([]string{"/bin/sh", "-c", command}, dockertest.ExecOptions{
		Env:    formatEnv(env),
		StdOut: &stdout,
		StdErr: &stderr,
	})
	output := stdout.String() + stderr.String()
	if err != nil {
		return output, fmt.Errorf("container command failed: %w", err)
	}
	if exitCode != 0 {
		return output, fmt.Errorf("container command exited with status %d", exitCode)
	}
	return output, ctx.Err()
}

func formatEnv(env map[string]string) []string {
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}
	return envSlice
}
