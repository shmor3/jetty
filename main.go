package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	defaultCommand  = "ps"
	defaultTimeout  = 30 * time.Second
	version         = "1.0.0"
	ErrInvalidInput = errors.New("invalid input")
	commands        = make(map[string]Command)
	logger          *log.Logger
)

type Config struct {
	Help    bool
	Verbose bool
	Version bool
}
type Command struct {
	Name        string
	Description string
	Usage       string
	Run         func(context.Context, []string) error
	MinArgs     int
	MaxArgs     int
	Subcommands map[string]*Command
	Flags       *flag.FlagSet
}

func init() {
	logger = log.New(os.Stderr, "", 0)
	registeredCommands()
	initializeGlobalWorkerPool(4)
}
func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Println("Received termination signal. Initiating graceful shutdown...")
		cancel()
	}()
	config, err := parseFlags()
	if err != nil {
		logger.Fatalf("Error: %v", err)
	}
	if config.Help {
		flag.Usage()
		return
	}
	if config.Version {
		logger.Printf("Version %s\n", version)
		return
	}
	if config.Verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
		logger.Println("Verbose mode enabled")
	} else {
		logger.SetFlags(0)
	}
	done := make(chan struct{})
	go func() {
		if err := handleSubcommands(ctx, os.Args[1:]); err != nil {
			if errors.Is(err, context.Canceled) {
				logger.Println("Operation canceled")
			} else {
				logger.Printf("Error: %v\n", err)
				flag.Usage()
			}
		}
		close(done)
	}()
	select {
	case <-ctx.Done():
		logger.Println("Waiting for ongoing operations to complete...")
		<-done
		logger.Println("Graceful shutdown complete")
	case <-done:
		logger.Println("All operations completed successfully")
	}
	logger.Println("Exiting program")
}
func customUsage() {
	logger.Printf("Usage: %s [options] [command]\n\n", os.Args[0])
	logger.Println("Options:")
	flag.PrintDefaults()
	logger.Println("\nCommands:")
	for _, cmd := range commands {
		logger.Printf("  %-10s %s\n", cmd.Name, cmd.Description)
	}
}
func handleSubcommands(ctx context.Context, args []string) error {
	verbose := false
	showHelp := false
	filteredArgs := []string{}
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			showHelp = true
		case "--verbose", "-v":
			verbose = true
		default:
			filteredArgs = append(filteredArgs, arg)
		}
	}
	if len(filteredArgs) == 0 {
		filteredArgs = append(filteredArgs, defaultCommand)
	}
	cmd, found := commands[filteredArgs[0]]
	if !found {
		return fmt.Errorf("%w: unknown command '%s'", ErrInvalidInput, filteredArgs[0])
	}
	if showHelp {
		return showCommandHelp(filteredArgs[0])
	}
	if verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
		logger.Println("Verbose mode enabled for command:", filteredArgs[0])
	}
	if err := validateArgs(cmd, filteredArgs[1:]); err != nil {
		return err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- cmd.Run(cmdCtx, filteredArgs[1:])
	}()
	select {
	case <-ctx.Done():
		logger.Println("Operation canceled")
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}
func showCommandHelp(cmdName string) error {
	cmd, found := commands[cmdName]
	if !found {
		return fmt.Errorf("%w: unknown command '%s'", ErrInvalidInput, cmdName)
	}
	logger.Printf("Usage: %s %s\n", os.Args[0], cmd.Usage)
	logger.Printf("Description: %s\n", cmd.Description)
	if len(cmd.Subcommands) > 0 {
		logger.Println("\nSubcommands:")
		for name, subcmd := range cmd.Subcommands {
			logger.Printf("  %-10s %s\n", name, subcmd.Description)
			logger.Printf("    Usage: %s %s %s\n", os.Args[0], cmdName, subcmd.Usage)
		}
	}
	return nil
}
func registerCommand(name string, cmd Command) {
	if cmd.Flags == nil {
		cmd.Flags = flag.NewFlagSet(name, flag.ContinueOnError)
	}
	cmd.Flags.Bool("verbose", false, "Enable verbose output")
	cmd.Flags.String("output", "", "Specify output format")
	originalRun := cmd.Run
	cmd.Run = func(ctx context.Context, args []string) error {
		if err := cmd.Flags.Parse(args); err != nil {
			return err
		}
		if cmd.Flags.Lookup("verbose").Value.(flag.Getter).Get().(bool) {
			logger.Println("Verbose mode enabled for command:", name)
		}
		return originalRun(ctx, cmd.Flags.Args())
	}
	for subName, subcmd := range cmd.Subcommands {
		if subcmd.Flags == nil {
			subcmd.Flags = flag.NewFlagSet(subName, flag.ContinueOnError)
		}
		subcmd.Flags.Bool("debug", false, "Enable debug mode")
		originalSubRun := subcmd.Run
		subcmd.Run = func(ctx context.Context, args []string) error {
			if err := subcmd.Flags.Parse(args); err != nil {
				return err
			}
			if subcmd.Flags.Lookup("debug").Value.(flag.Getter).Get().(bool) {
				logger.Println("Debug mode enabled for subcommand:", subName)
			}
			return originalSubRun(ctx, subcmd.Flags.Args())
		}
	}
	commands[name] = cmd
}
