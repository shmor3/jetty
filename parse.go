package main

import (
	"bufio"
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
	var instructions []Instruction
	scanner := bufio.NewScanner(file)
	var multiLineCommand string
	var multiLineStart int
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmedLine := strings.TrimSpace(line)
		if multiLineCommand == "" && (trimmedLine == "" || strings.HasPrefix(trimmedLine, "#")) {
			continue
		}
		lineWithoutTrailingSpace := strings.TrimRight(line, " \t")
		if strings.HasSuffix(lineWithoutTrailingSpace, "\\") {
			if multiLineCommand == "" {
				multiLineStart = lineNumber
			}
			multiLineCommand += strings.TrimSuffix(lineWithoutTrailingSpace, "\\") + "\n"
			continue
		}
		instructionLineNumber := lineNumber
		if multiLineCommand != "" {
			line = multiLineCommand + line
			multiLineCommand = ""
			instructionLineNumber = multiLineStart
		}
		line = strings.TrimSpace(line)
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("line %d: invalid instruction: %s", instructionLineNumber, line)
		}
		token := parts[0]
		argsStart := strings.Index(line, token) + len(token)
		directive, symbol, err := parseDirectiveToken(token)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", instructionLineNumber, err)
		}
		instructions = append(instructions, Instruction{
			Directive: directive,
			Symbol:    symbol,
			Args:      strings.TrimSpace(line[argsStart:]),
			Line:      instructionLineNumber,
		})
	}
	if multiLineCommand != "" {
		return nil, fmt.Errorf("line %d: unterminated multi-line command", multiLineStart)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return instructions, nil
}

var directiveSymbols = map[string]map[string]bool{
	"ARG": {"": true},
	"ENV": {"": true},
	"RUN": {"": true, "*": true},
	"CMD": {"": true},
	"DIR": {"": true},
	"CPY": {"": true, "*": true},
	"WDR": {"": true},
	"SUB": {"": true, "*": true},
	"FRM": {"": true},
	"JET": {"": true},
	"FMT": {"": true, "^": true, "$": true, "&": true},
	"BOX": {"": true},
	"USE": {"": true},
}

func parseDirectiveToken(token string) (string, string, error) {
	if token == "" {
		return "", "", fmt.Errorf("empty directive")
	}
	symbol := ""
	directive := token
	if strings.ContainsRune("*^$&", rune(token[0])) {
		symbol = token[:1]
		directive = token[1:]
	}
	allowedSymbols, ok := directiveSymbols[directive]
	if !ok || directive == "" {
		return "", "", fmt.Errorf("invalid directive: %s", token)
	}
	if !allowedSymbols[symbol] {
		if symbol == "" {
			return "", "", fmt.Errorf("directive %s requires a supported modifier", directive)
		}
		return "", "", fmt.Errorf("modifier %s is not supported for directive %s", symbol, directive)
	}
	return directive, symbol, nil
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
	args := os.Args[1:]
scan:
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			config.Help = true
		case "--verbose", "-v":
			config.Verbose = true
		case "--version":
			config.Version = true
		default:
			break scan
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
