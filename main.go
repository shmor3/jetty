package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

var (
	defaultCommand = "status"
	defaultTimeout = 10 * time.Minute
	version        = "1.0.0"
	// ErrInvalidInput indicates the user supplied an unknown command or bad arguments.
	ErrInvalidInput = errors.New("invalid input")
	commands        = make(map[string]Command)
	logger          *log.Logger
	// stdout receives queryable command output (version string, status tables,
	// build output). Diagnostics, warnings, and errors go to logger (stderr).
	stdout io.Writer = os.Stdout
)

// Config holds the global flags parsed before command dispatch.
type Config struct {
	Help    bool
	Verbose bool
	Version bool
}

// Command describes a registered CLI command and its argument constraints.
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
	flag.Usage = customUsage
	if t := os.Getenv("JETTY_TIMEOUT"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			defaultTimeout = d
		} else {
			logger.Printf("Warning: invalid JETTY_TIMEOUT %q, using default %v", t, defaultTimeout)
		}
	}
	registerCommands()
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
	config := parseFlags()
	if config.Help {
		flag.Usage()
		return
	}
	if config.Version {
		fmt.Fprintf(stdout, "Jetty version %s\n", version)
		return
	}
	if config.Verbose {
		logger.SetFlags(log.LstdFlags | log.Lshortfile)
		logger.Println("Verbose mode enabled")
	} else {
		logger.SetFlags(0)
	}
	if err := handleSubcommands(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Println("Operation canceled")
		} else {
			logger.Printf("Error: %v\n", err)
		}
		os.Exit(1)
	}
}

func customUsage() {
	logger.Printf("Usage: %s [options] [command]\n\n", os.Args[0])
	logger.Println("Options:")
	logger.Println("  -h, --help       Show help message")
	logger.Println("  -v, --verbose    Enable verbose output")
	logger.Println("  --version        Show version information")
	logger.Println("\nCommands:")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cmd := commands[name]
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
	return cmd.Run(cmdCtx, filteredArgs[1:])
}

func showCommandHelp(cmdName string) error {
	cmd, found := commands[cmdName]
	if !found {
		return fmt.Errorf("%w: unknown command '%s'", ErrInvalidInput, cmdName)
	}
	logger.Printf("Usage: %s %s\n", os.Args[0], cmd.Usage)
	logger.Printf("Description: %s\n", cmd.Description)
	if cmd.Flags != nil {
		logger.Println("\nOptions:")
		cmd.Flags.SetOutput(os.Stderr)
		cmd.Flags.PrintDefaults()
	}
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
	for _, subcmd := range cmd.Subcommands {
		if subcmd.Flags == nil {
			subcmd.Flags = flag.NewFlagSet(subcmd.Name, flag.ContinueOnError)
		}
	}
	commands[name] = cmd
}
