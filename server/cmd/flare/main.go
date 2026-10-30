// Command flare is the Flare server and its command-line client.
//
//	flare serve                      run the server
//	flare publish backups "done"     send a message
//	flare subscribe backups          print messages as they arrive
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Ctrl-C and `docker stop` ask for a clean shutdown: the context ends, and
	// the server finishes what it is doing.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := newRootCmd(cli{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv})
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
