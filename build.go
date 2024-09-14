package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type Instruction struct {
	Directive string
	Args      string
}

func build(fileName string, resultChan chan<- string) error {
	if fileName == "" {
		return fmt.Errorf("please provide a file name")
	}
	instructions, err := parseFile(fileName)
	if err != nil {
		return fmt.Errorf("error parsing file: %v", err)
	}
	args := make(map[string]string)
	env := make(map[string]string)
	var cmdInstruction *Instruction
	for _, inst := range instructions {
		if inst.Directive == "CMD" {
			if cmdInstruction != nil {
				return fmt.Errorf("multiple CMD directives are not allowed")
			}
			cmdInstruction = &inst
			continue
		}
		err := executeInstruction(inst, args, resultChan)
		if err != nil {
			return fmt.Errorf("error executing instruction: %v", err)
		}
	}
	if cmdInstruction != nil {
		err := executeCMD(*cmdInstruction, env, resultChan)
		if err != nil {
			return fmt.Errorf("error executing CMD instruction: %v", err)
		}
	}
	close(resultChan)
	return nil
}

func executeCMD(inst Instruction, env map[string]string, resultChan chan<- string) error {
	cmd := exec.Command("sh", "-c", inst.Args)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("CMD execution failed: %v", err)
	}
	resultChan <- fmt.Sprintf("CMD: %s", inst.Args)
	resultChan <- fmt.Sprintf("Done: %s\n", string(output))
	return nil
}

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

func executeInstruction(inst Instruction, args map[string]string, resultChan chan<- string) error {
	logMessage := func(format string, v ...interface{}) {
		msg := fmt.Sprintf(format, v...)
		resultChan <- msg + "\n"
	}
	switch inst.Directive {
	case "ARG":
		parts := strings.SplitN(inst.Args, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ARG format: %s", inst.Args)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.Contains(key, " ") {
			return fmt.Errorf("only one ARG allowed per directive: %s", inst.Args)
		}
		args[key] = expandArgs(value, args)
	case "ENV":
		parts := strings.SplitN(inst.Args, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid ENV format: %s", inst.Args)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if strings.Contains(key, " ") {
			return fmt.Errorf("only one ENV allowed per directive: %s", inst.Args)
		}
		expandedValue := expandArgs(value, args)
		os.Setenv(key, expandedValue)
		logMessage("ENV: %s=%s\n", key, expandedValue)
	case "RUN":
		expandedArgs := expandArgs(inst.Args, args)
		if err := validateLinuxCommand(expandedArgs); err != nil {
			return fmt.Errorf("invalid RUN command: %v", err)
		}
		cmd := exec.Command("sh", "-c", expandedArgs)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command execution failed: %v", err)
		}
		logMessage("Done: %s\n", string(output))
	case "DIR":
		expandedArgs := expandArgs(inst.Args, args)
		err := os.MkdirAll(expandedArgs, 0755)
		if err != nil {
			return fmt.Errorf("directory creation failed: %v", err)
		}
		logMessage("Done: %s\n", "directory created")
	case "WDR":
		parts := strings.Fields(inst.Args)
		if len(parts) != 1 {
			return fmt.Errorf("only one directory allowed per WDR directive: %s", inst.Args)
		}
		expandedDir := expandArgs(parts[0], args)
		if _, err := os.Stat(expandedDir); os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", expandedDir)
		}
		err := os.Chdir(expandedDir)
		if err != nil {
			return fmt.Errorf("failed to change directory: %v", err)
		}
		logMessage("WDR: Changed working directory to %s", expandedDir)
	default:
		return fmt.Errorf("unknown directive: %s", inst.Directive)
	}
	return nil
}

func validateLinuxCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return fmt.Errorf("empty command")
	}
	for pattern, message := range disallowedPatterns {
		if matched, _ := regexp.MatchString(pattern, cmd); matched {
			return fmt.Errorf("command contains %s, which is not allowed", message)
		}
	}
	if strings.Count(cmd, "'")%2 != 0 {
		return fmt.Errorf("unmatched single quotes in command")
	}
	if strings.Count(cmd, "\"")%2 != 0 {
		return fmt.Errorf("unmatched double quotes in command")
	}
	return nil
}

var disallowedPatterns = map[string]string{
	`^\||\|$`:   "command begins or ends with a pipe '|'",
	`\|\|`:      "OR operator '||'",
	`&&`:        "AND operator '&&'",
	"`":         "backticks '`'",
	`#`:         "comments '#'",
	`;`:         "semicolons ';'",
	`>|>>`:      "output redirection '>' or '>>'",
	`<|<<`:      "input redirection '<' or '<<'",
	`&`:         "background execution operator '&'",
	`\$\(|\)`:   "command substitution '$(...)'",
	`{|}`:       "brace expansion '{}'",
	`\[\[|\]\]`: "conditional expression '[[...]]'",
	`export|source|\.|sudo|eval|exec|alias|function`: "disallowed keywords",
	`if|then|else|fi|for|while|do|done|case|esac`:    "control structures",
	`~`:              "tilde '~' for home directory expansion",
	`\\`:             "backslash '\\'",
	`\$\{.*\}`:       "variable expansion '${...}'",
	`\(\(.*\)\)`:     "arithmetic expansion '(())'",
	`:[p]?[:=?+.-]`:  "parameter expansion operators",
	`\btime\b`:       "'time' command prefix",
	`\bnohup\b`:      "'nohup' command prefix",
	`\bxargs\b`:      "'xargs' command",
	`\benv\b`:        "'env' command",
	`\bnice\b`:       "'nice' command prefix",
	`\btrap\b`:       "'trap' command",
	`\bcommand\b`:    "'command' built-in",
	`\bset\b`:        "'set' built-in",
	`\bunset\b`:      "'unset' built-in",
	`\bwait\b`:       "'wait' built-in",
	`\bkill\b`:       "'kill' command",
	`\bcron\b`:       "cron-related commands",
	`\bat\b`:         "'at' command",
	`\bchmod\b`:      "'chmod' command",
	`\bchown\b`:      "'chown' command",
	`\bchgrp\b`:      "'chgrp' command",
	`\bmkdir\b`:      "'mkdir' command",
	`\brm\b`:         "'rm' command",
	`\bmv\b`:         "'mv' command",
	`\bcp\b`:         "'cp' command",
	`\bln\b`:         "'ln' command",
	`\btouch\b`:      "'touch' command",
	`\bdd\b`:         "'dd' command",
	`\bfind\b`:       "'find' command",
	`\bgrep\b`:       "'grep' command",
	`\bsed\b`:        "'sed' command",
	`\bawk\b`:        "'awk' command",
	`\bperl\b`:       "'perl' command",
	`\bpython\b`:     "'python' command",
	`\bruby\b`:       "'ruby' command",
	`\bcurl\b`:       "'curl' command",
	`\bwget\b`:       "'wget' command",
	`\bnc\b`:         "'nc' (netcat) command",
	`\bnetstat\b`:    "'netstat' command",
	`\bss\b`:         "'ss' command",
	`\biptables\b`:   "'iptables' command",
	`\bufw\b`:        "'ufw' command",
	`\bsystemctl\b`:  "'systemctl' command",
	`\bservice\b`:    "'service' command",
	`\bjournalctl\b`: "'journalctl' command",
	`\blogin\b`:      "'login' command",
	`\bsu\b`:         "'su' command",
	`\bpasswd\b`:     "'passwd' command",
	`\buseradd\b`:    "'useradd' command",
	`\buserdel\b`:    "'userdel' command",
	`\bmodprobe\b`:   "'modprobe' command",
	`\binsmod\b`:     "'insmod' command",
	`\brmmod\b`:      "'rmmod' command",
	`\bdmesg\b`:      "'dmesg' command",
	`\bbase64\b`:     "'base64' command",
}
