// Package app связывает CLI с движком: аргументы, анализ, отчет, файл и exit code
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/avequa/incident-radar/internal/analyze"
	"github.com/avequa/incident-radar/internal/cli"
	"github.com/avequa/incident-radar/internal/output"
)

// Коды завершения программы
const (
	ExitOK       = 0
	ExitInternal = 1
	ExitUsage    = 2
)

// Streams - стандартные потоки процесса
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// writeFunc - функция сохранения отчета, в тестах подменяется на функцию с ошибкой
type writeFunc func(path string, data []byte) error

// Run - запускает CLI и возвращает exit code
func Run(ctx context.Context, args []string, streams Streams) int {
	return run(ctx, args, streams, output.WriteNew)
}

// run - логика Run с подменяемой функцией записи отчета; паника превращается в exit code 1
func run(ctx context.Context, args []string, streams Streams, writeOutput writeFunc) (code int) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(streams.Stderr, "internal error: %v\n", r)
			code = ExitInternal
		}
	}()

	in, err := cli.ParseCliFlag(args, streams.Stderr)
	if err != nil {
		return reportError(streams.Stderr, err)
	}

	rep, err := analyze.Run(ctx, options(in, streams))
	if err != nil {
		return reportError(streams.Stderr, err)
	}

	var buf bytes.Buffer
	if err = writeReport(&buf, in.Format, rep); err != nil {
		return reportError(streams.Stderr, err)
	}

	if in.Output == "" {
		if _, err = streams.Stdout.Write(buf.Bytes()); err != nil {
			return reportError(streams.Stderr, err)
		}
		return ExitOK
	}

	if err = writeOutput(in.Output, buf.Bytes()); err != nil {
		return reportError(streams.Stderr, err)
	}
	return ExitOK
}

// options - переводит параметры CLI в опции движка
func options(in cli.UserInput, streams Streams) analyze.Options {
	return analyze.Options{
		Path:          in.Path,
		From:          in.From,
		To:            in.To,
		CompareFrom:   in.CompareFrom,
		CompareTo:     in.CompareTo,
		Since:         in.Since,
		Until:         in.Until,
		Bucket:        in.Bucket,
		Top:           in.Top,
		Stdin:         streams.Stdin,
		Warnings:      streams.Stderr,
		QuietWarnings: in.Quiet,
	}
}

// writeReport - пишет отчет в выбранном формате, неизвестный формат - внутренняя ошибка
func writeReport(w io.Writer, format string, rep analyze.Report) error {
	switch format {
	case cli.FormatJSON:
		return analyze.WriteJSON(w, rep)
	case cli.FormatMarkdown:
		return analyze.WriteMarkdown(w, rep)
	case cli.FormatAdoc:
		return analyze.WriteAdoc(w, rep)
	default:
		return fmt.Errorf("unknown report format %q", format)
	}
}

// reportError - пишет ошибку в stderr и выбирает exit code: ожидаемые ошибки - 2, остальные - 1
func reportError(stderr io.Writer, err error) int {
	if errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(stderr, "error: interrupted")
		return ExitInternal
	}
	_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	if errors.Is(err, cli.ErrUsage) || errors.Is(err, output.ErrExists) || analyze.IsUsageError(err) {
		return ExitUsage
	}
	return ExitInternal
}
