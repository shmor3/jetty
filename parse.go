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
	validDirectives := map[string]bool{
		"ARG": true, "ENV": true, "RUN": true, "CMD": true, "DIR": true, "WDR": true,
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
		if !validDirectives[directive] {
			return nil, fmt.Errorf("invalid directive: %s", directive)
		}
		instructions = append(instructions, Instruction{
			Directive: directive,
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
