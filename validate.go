package main

import (
	"fmt"
	"regexp"
	"strings"
)

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
