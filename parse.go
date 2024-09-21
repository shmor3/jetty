package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
)

func parseFile(fileName string) ([]Instruction, error) {
	file, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	validDirectives := map[string][]string{
		"ARG": {},
		"ENV": {},
		"RUN": {"*"},
		"CMD": {},
		"DIR": {},
		"CPY": {"*"},
		"WDR": {},
		"SUB": {"*"},
		"FRM": {},
		"JET": {},
		"FMT": {"^", "$", "&"},
		"BOX": {},
		"USE": {},
	}
	var instructions []Instruction
	scanner := bufio.NewScanner(file)
	var multiLineCommand string
	for scanner.Scan() {
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" || strings.HasPrefix(trimmedLine, "#") {
			continue
		}
		if strings.HasSuffix(line, "\\") {
			multiLineCommand += strings.TrimSuffix(line, "\\") + "\n"
			continue
		}
		if multiLineCommand != "" {
			line = multiLineCommand + line
			multiLineCommand = ""
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid instruction: %s", line)
		}
		directive := parts[0]
		prefix := ""
		if strings.HasPrefix(directive, "*") {
			prefix = "*"
			directive = directive[1:]
		}
		if _, ok := validDirectives[directive]; !ok {
			return nil, fmt.Errorf("invalid directive: %s", prefix+directive)
		}
		instructions = append(instructions, Instruction{
			Directive: prefix + directive,
			Args:      strings.TrimSpace(parts[1]),
		})
	}
	if multiLineCommand != "" {
		return nil, fmt.Errorf("unterminated multi-line command")
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return instructions, nil
}
func expandArgs(s string, args map[string]string) string {
	return os.Expand(s, func(k string) string {
		if v, ok := args[k]; ok {
			return v
		}
		return "$" + k
	})
}
func parseFlags() (*Config, error) {
	config := &Config{}
	flag.BoolVar(&config.Help, "help", false, "Show help message")
	flag.BoolVar(&config.Help, "h", false, "Show help message (shorthand)")
	flag.BoolVar(&config.Verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&config.Verbose, "v", false, "Enable verbose output (shorthand)")
	flag.BoolVar(&config.Version, "version", false, "Show version information")
	flag.Usage = customUsage
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) > 1 && arg[0] == '-' {
			if len(arg) > 2 && arg[1] == '-' {
				name := arg[2:]
				if f := flag.Lookup(name); f != nil {
					f.Value.Set("true")
				}
			} else {
				name := arg[1:]
				if f := flag.Lookup(name); f != nil {
					f.Value.Set("true")
				}
			}
		} else {
			break
		}
	}
	return config, nil
}
func validateArgs(cmd Command, args []string) error {
	if len(args) < cmd.MinArgs {
		return fmt.Errorf("%w: not enough arguments for command '%s'", ErrInvalidInput, cmd.Name)
	}
	if cmd.MaxArgs > 0 && len(args) > cmd.MaxArgs {
		return fmt.Errorf("%w: too many arguments for command '%s'", ErrInvalidInput, cmd.Name)
	}
	return nil
}
