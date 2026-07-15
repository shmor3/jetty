package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

func registeredCommands() {
	registerCommand("help", Command{
		Name:        "help",
		Description: "Show help for a command",
		Usage:       "help [command]",
		Run: func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				customUsage()
				return nil
			}
			return showCommandHelp(args[0])
		},
		MinArgs: 0,
		MaxArgs: 1,
	})
	registerCommand("version", Command{
		Name:        "version",
		Description: "Show version information",
		Usage:       "version",
		Run: func(ctx context.Context, args []string) error {
			logger.Printf("Jetty version %s\n", version)
			return nil
		},
		MinArgs: 0,
		MaxArgs: 0,
	})
	registerCommand("init", Command{
		Name:        "init",
		Description: "Create a new Jettyfile in the current directory",
		Usage:       "init",
		Run: func(ctx context.Context, args []string) error {
			if len(args) != 0 {
				return fmt.Errorf("%w: init does not accept arguments", ErrInvalidInput)
			}
			if _, err := os.Stat("Jettyfile"); err == nil {
				return fmt.Errorf("Jettyfile already exists")
			} else if !os.IsNotExist(err) {
				return fmt.Errorf("failed to inspect Jettyfile: %w", err)
			}
			file, err := os.OpenFile("Jettyfile", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
			if err != nil {
				return fmt.Errorf("failed to create Jettyfile: %w", err)
			}
			defer file.Close()
			if _, err = file.WriteString("# Jettyfile\n\n# Add your build instructions here\n"); err != nil {
				return fmt.Errorf("failed to write to Jettyfile: %w", err)
			}
			logger.Println("Jettyfile created successfully in the current directory")
			return nil
		},
		MinArgs: 0,
		MaxArgs: 0,
	})
	registerCommand("ps", Command{
		Name:        "ps",
		Description: "View active builds",
		Usage:       "ps [-a] [-f filter]",
		Run:         runStatusCommand("ps", false),
		MinArgs:     0,
		MaxArgs:     3,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("ps", flag.ContinueOnError)
			fs.Bool("a", false, "Show all builds")
			fs.Bool("active", false, "Show only active builds")
			fs.String("f", "", "Filter builds")
			return fs
		}(),
	})
	registerCommand("status", Command{
		Name:        "status",
		Description: "View build status history",
		Usage:       "status [--active] [-f filter]",
		Run:         runStatusCommand("status", true),
		MinArgs:     0,
		MaxArgs:     3,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("status", flag.ContinueOnError)
			fs.Bool("a", false, "Show all builds")
			fs.Bool("active", false, "Show only active builds")
			fs.String("f", "", "Filter builds")
			return fs
		}(),
	})
	registerCommand("build", Command{
		Name:        "build",
		Description: "Run a new build",
		Usage:       "build [-f filename] [filename]",
		Run: func(ctx context.Context, args []string) error {
			fs := flag.NewFlagSet("build", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			fileFlag := fs.String("f", "", "Specify the build file")
			if err := fs.Parse(args); err != nil {
				return err
			}
			fileName := *fileFlag
			if fileName == "" && fs.NArg() > 0 {
				fileName = fs.Arg(0)
			}
			if fs.NArg() > 1 || (fileName != "" && *fileFlag != "" && fs.NArg() > 0) {
				return fmt.Errorf("%w: build accepts either -f or one positional file", ErrInvalidInput)
			}
			if fileName == "" {
				if _, err := os.Stat("Jettyfile"); err == nil {
					fileName = "Jettyfile"
				} else if os.IsNotExist(err) {
					return fmt.Errorf("no Jettyfile found in current directory and no file specified")
				} else {
					return fmt.Errorf("failed to inspect Jettyfile: %w", err)
				}
			}

			resultChan := make(chan string)
			buildInfoChan := make(chan BuildInfo)
			errChan := make(chan error, 1)
			buildID := fmt.Sprintf("%d", time.Now().UnixNano())
			workerNode := "local"
			var lastBuildInfo BuildInfo

			go func() {
				errChan <- build(ctx, fileName, buildID, workerNode, resultChan, buildInfoChan)
			}()

			resultOpen := true
			infoOpen := true
			for resultOpen || infoOpen {
				select {
				case result, ok := <-resultChan:
					if !ok {
						resultOpen = false
						continue
					}
					if logger.Flags()&log.LstdFlags != 0 {
						logger.Printf("Build: %s", result)
					} else {
						fmt.Println(result)
					}
				case buildInfo, ok := <-buildInfoChan:
					if !ok {
						infoOpen = false
						continue
					}
					lastBuildInfo = buildInfo
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			if err := <-errChan; err != nil {
				return err
			}
			logger.Printf("Build %s: Status: %s, Worker: %s",
				lastBuildInfo.ID, lastBuildInfo.Status, lastBuildInfo.WorkerNode)
			return nil
		},
		MinArgs: 0,
		MaxArgs: 2,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("build", flag.ContinueOnError)
			fs.String("f", "", "Specify the build file")
			return fs
		}(),
	})
}

func runStatusCommand(name string, defaultAll bool) func(context.Context, []string) error {
	return func(ctx context.Context, args []string) error {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		allFlag := fs.Bool("a", false, "Show all builds")
		activeFlag := fs.Bool("active", false, "Show only active builds")
		filterFlag := fs.String("f", "", "Filter builds, e.g. id=buildid, status=Failed, worker=local, file=Jettyfile")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if fs.NArg() != 0 {
			return fmt.Errorf("%w: %s does not accept positional arguments: %s", ErrInvalidInput, name, strings.Join(fs.Args(), " "))
		}

		showAll := defaultAll || *allFlag
		if *activeFlag {
			showAll = false
		}
		builds, err := readBuildInfos()
		if err != nil {
			return fmt.Errorf("failed to read build status: %w", err)
		}
		filtered := filterBuildInfos(builds, showAll, *filterFlag)
		if len(filtered) == 0 {
			printEmptyStatusMessage(name, showAll, len(builds) > 0)
			return nil
		}
		sortBuildInfos(filtered)
		printBuildInfos(filtered)
		return nil
	}
}

func printEmptyStatusMessage(command string, showAll bool, hasHistory bool) {
	if showAll {
		logger.Println("No builds found.")
		return
	}
	if hasHistory {
		logger.Printf("No active builds found. Use `jetty %s -a` or `jetty status` to show completed builds.", command)
		return
	}
	logger.Println("No active builds found.")
}

func sortBuildInfos(builds []BuildInfo) {
	sort.Slice(builds, func(i, j int) bool {
		return builds[i].StartTime.After(builds[j].StartTime)
	})
}

func printBuildInfos(builds []BuildInfo) {
	writer := tabwriter.NewWriter(logger.Writer(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tSTATUS\tWORKER\tSTART\tEND\tFILE\tERROR")
	for _, info := range builds {
		end := "-"
		if !info.EndTime.IsZero() {
			end = info.EndTime.Format(time.RFC3339)
		}
		
		idStr := info.ID
		if len(idStr) > 25 {
			idStr = "..." + idStr[len(idStr)-22:]
		}
		
		errStr := info.Error
		if len(errStr) > 50 {
			errStr = errStr[:47] + "..."
		}
		
		fileName := info.FileName
		if len(fileName) > 35 {
			fileName = "..." + fileName[len(fileName)-32:]
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			idStr,
			info.Status,
			info.WorkerNode,
			info.StartTime.Format(time.RFC3339),
			end,
			fileName,
			errStr,
		)
	}
	if err := writer.Flush(); err != nil {
		logger.Printf("Warning: failed to render build status: %v", err)
	}
}

func filterBuildInfos(builds []BuildInfo, all bool, filter string) []BuildInfo {
	filter = strings.TrimSpace(filter)
	filtered := make([]BuildInfo, 0, len(builds))
	for _, info := range builds {
		if !all && info.Status != statusRunning {
			continue
		}
		if filter != "" && !matchesBuildFilter(info, filter) {
			continue
		}
		filtered = append(filtered, info)
	}
	return filtered
}

func matchesBuildFilter(info BuildInfo, filter string) bool {
	key, value, hasKey := strings.Cut(filter, "=")
	if hasKey {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "id":
			return info.ID == value
		case "status":
			return strings.EqualFold(info.Status, value)
		case "worker", "worker_node":
			return info.WorkerNode == value
		case "file", "filename":
			return strings.Contains(info.FileName, value)
		default:
			return false
		}
	}
	return info.ID == filter ||
		strings.EqualFold(info.Status, filter) ||
		info.WorkerNode == filter ||
		strings.Contains(info.FileName, filter)
}
