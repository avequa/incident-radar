// Package main - CLI incident-radar: статистика по NGINX-логам в JSON, Markdown или AsciiDoc
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/avequa/incident-radar/internal/app"
)

// main - запускает CLI, Ctrl+C и SIGTERM отменяют чтение
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := app.Run(ctx, os.Args[1:], app.Streams{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	stop()
	os.Exit(code)
}
