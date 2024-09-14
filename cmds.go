package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
)

func registeredCommands() {
	registerCommand("status", Command{
		Name:        "status",
		Description: "Show the current status of the application",
		Usage:       "status [subcommand] [args...]",
		Run: func(ctx context.Context, args []string) error {
			if len(args) == 0 {
				logger.Println("Application is running normally.")
				return nil
			}
			subcommand := args[0]
			if subcmd, exists := commands["status"].Subcommands[subcommand]; exists {
				return subcmd.Run(ctx, args[1:])
			}
			return fmt.Errorf("unknown subcommand: %s", subcommand)
		}, MinArgs: 0,
		MaxArgs: 2,
		Subcommands: map[string]*Command{
			"detail": {
				Name:        "detail",
				Description: "Show detailed status information",
				Usage:       "status detail",
				Run: func(ctx context.Context, args []string) error {
					logger.Println("Detailed status: all systems operational")
					return nil
				},
				MinArgs: 0,
				MaxArgs: 0,
				Flags: func() *flag.FlagSet {
					fs := flag.NewFlagSet("system", flag.ExitOnError)
					fs.Bool("system", false, "Show system status")
					return fs
				}(),
			},
		},
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("all", flag.ExitOnError)
			fs.Bool("all", false, "Show system status")
			return fs
		}(),
	})
	registerCommand("build", Command{
		Name:        "build",
		Description: "Run a new build",
		Usage:       "build",
		Run: func(ctx context.Context, args []string) error {
			var fileName string
			if len(args) > 0 {
				fileName = args[0]
			} else {
				// Check if Jettyfile exists in the current directory
				if _, err := os.Stat("Jettyfile"); err == nil {
					fileName = "Jettyfile"
				} else {
					return fmt.Errorf("no Jettyfile found in current directory and no file specified")
				}
			}
			resultChan := make(chan string)
			go func() {
				for result := range resultChan {
					if logger.Flags()&log.LstdFlags != 0 {
						logger.Printf("Build: %s", result)
					} else {
						fmt.Println(result)
					}
				}
			}()
			return build(fileName, resultChan)
		},
		MinArgs: 0,
		MaxArgs: 0,
	})
}
