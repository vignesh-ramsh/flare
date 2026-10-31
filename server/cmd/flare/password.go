package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// maxPasswordInput bounds how much is read when a password comes from a pipe.
const maxPasswordInput = 4096

// readPassword reads a password without it ever appearing on a command line, where
// other users could see it in the process list and it would be kept in shell
// history. On a terminal it prompts and doesn't echo; otherwise it reads one line
// from standard input, so that a script can pipe it in. With confirm set, a
// terminal prompts twice.
func readPassword(c cli, prompt string, confirm bool) (string, error) {
	if f, ok := c.stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		ask := func(p string) (string, error) {
			fmt.Fprint(c.stderr, p)
			b, err := term.ReadPassword(int(f.Fd()))
			fmt.Fprintln(c.stderr)
			return string(b), err
		}
		first, err := ask(prompt)
		if err != nil {
			return "", err
		}
		if confirm {
			again, err := ask("Again: ")
			if err != nil {
				return "", err
			}
			if again != first {
				return "", errors.New("the two passwords differ")
			}
		}
		return first, nil
	}

	line, err := bufio.NewReader(io.LimitReader(c.stdin, maxPasswordInput)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read the password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return "", errors.New("no password given: type it at the prompt, or pipe it in on standard input")
	}
	return password, nil
}
