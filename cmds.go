package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

func registeredCommands() {
	registerCommand("init", Command{
		Name:        "init",
		Description: "Create a new Jettyfile in the current directory",
		Usage:       "init",
		Run: func(ctx context.Context, args []string) error {
			file, err := os.Create("Jettyfile")
			if err != nil {
				return fmt.Errorf("failed to create Jettyfile: %v", err)
			}
			defer file.Close()
			_, err = file.WriteString("# Jettyfile\n\n# Add your build instructions here\n")
			if err != nil {
				return fmt.Errorf("failed to write to Jettyfile: %v", err)
			}
			logger.Println("Jettyfile created successfully in the current directory")
			return nil
		},
		MinArgs: 0,
		MaxArgs: 0,
	})
	registerCommand("ps", Command{
		Name:        "ps",
		Description: "View the status of builds",
		Usage:       "jettyctl ps [-a] [-f filter]",
		Run: func(ctx context.Context, args []string) error {
			fs := flag.NewFlagSet("ps", flag.ContinueOnError)
			allFlag := fs.Bool("a", false, "Show all builds (active and completed)")
			filterFlag := fs.String("f", "", "Filter builds (e.g., \"id=buildid\")")
			if err := fs.Parse(args); err != nil {
				return err
			}
			buildInfoChan := make(chan BuildInfo)
			outputChan := make(chan map[string]BuildInfo)
			go listActiveBuilds(buildInfoChan, outputChan)
			builds := <-outputChan
			if *allFlag {
				fmt.Println("All builds (active and completed):")
			} else {
				fmt.Println("Active builds:")
			}
			matchesFilter := func(id string, info BuildInfo, filter string) bool {
				return id == filter || info.Status == filter || info.WorkerNode == filter
			}
			for id, info := range builds {
				if (*allFlag || info.Status == "Running") && (*filterFlag == "" || matchesFilter(id, info, *filterFlag)) {
					fmt.Printf("Build ID: %s, Status: %s, Worker: %s, Start Time: %s\n",
						id, info.Status, info.WorkerNode, info.StartTime)
				}
			}
			return nil
		},
		MinArgs: 0,
		MaxArgs: 2,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("ps", flag.ExitOnError)
			fs.Bool("a", false, "Show all builds (active and completed)")
			fs.String("f", "", "Filter builds (e.g., \"id=buildid\")")
			return fs
		}(),
	})
	registerCommand("build", Command{
		Name:        "build",
		Description: "Run a new build",
		Usage:       "jettyctl build -f filename",
		Run: func(ctx context.Context, args []string) error {
			fs := flag.NewFlagSet("build", flag.ContinueOnError)
			fileFlag := fs.String("f", "", "Specify the build file")
			if err := fs.Parse(args); err != nil {
				return err
			}
			var fileName string
			if *fileFlag != "" {
				fileName = *fileFlag
			} else if fs.NArg() > 0 {
				fileName = fs.Arg(0)
			} else {
				if _, err := os.Stat("Jettyfile"); err == nil {
					fileName = "Jettyfile"
				} else {
					return fmt.Errorf("no Jettyfile found in current directory and no file specified")
				}
			}
			resultChan := make(chan string)
			buildInfoChan := make(chan BuildInfo)
			go func() {
				for result := range resultChan {
					if logger.Flags()&log.LstdFlags != 0 {
						logger.Printf("Build: %s", result)
					} else {
						fmt.Println(result)
					}
				}
			}()
			buildID := fmt.Sprintf("build-%d", time.Now().UnixNano())
			workerNode := "default-worker"
			go build(fileName, buildID, workerNode, resultChan, buildInfoChan)
			return nil
		},
		MinArgs: 0,
		MaxArgs: 2,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("build", flag.ExitOnError)
			fs.String("f", "", "Specify the build file")
			return fs
		}(),
	})
}
