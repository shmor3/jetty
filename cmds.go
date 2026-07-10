package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

func registeredCommands() {
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
		Description: "View build status history",
		Usage:       "ps [-a] [-f filter]",
		Run: func(ctx context.Context, args []string) error {
			fs := flag.NewFlagSet("ps", flag.ContinueOnError)
			fs.SetOutput(os.Stderr)
			allFlag := fs.Bool("a", false, "Show all builds instead of only active builds")
			filterFlag := fs.String("f", "", "Filter builds, e.g. id=buildid, status=Failed, worker=local, file=Jettyfile")
			if err := fs.Parse(args); err != nil {
				return err
			}
			builds, err := readBuildInfos()
			if err != nil {
				return fmt.Errorf("failed to read build status: %w", err)
			}
			builds = filterBuildInfos(builds, *allFlag, *filterFlag)
			if len(builds) == 0 {
				if *allFlag {
					logger.Println("No builds found.")
				} else {
					logger.Println("No active builds found.")
				}
				return nil
			}
			sort.Slice(builds, func(i, j int) bool {
				return builds[i].StartTime.After(builds[j].StartTime)
			})
			for _, info := range builds {
				end := "-"
				if !info.EndTime.IsZero() {
					end = info.EndTime.Format(time.RFC3339)
				}
				logger.Printf("%s\t%s\t%s\t%s\t%s\t%s",
					info.ID,
					info.Status,
					info.WorkerNode,
					info.StartTime.Format(time.RFC3339),
					end,
					info.FileName,
				)
			}
			return nil
		},
		MinArgs: 0,
		MaxArgs: 3,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("ps", flag.ContinueOnError)
			fs.Bool("a", false, "Show all builds instead of only active builds")
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
