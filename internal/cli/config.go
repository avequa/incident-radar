// Package cli разбирает и проверяет аргументы командной строки
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrUsage - ошибка использования: неверный флаг, формат, дата или output
var ErrUsage = errors.New("usage error")

// Поддерживаемые форматы отчета
const (
	FormatJSON     string = "json"
	FormatMarkdown string = "markdown"
	FormatAdoc     string = "adoc"
)

// StdinPath - значение --path для чтения лога из stdin
const StdinPath = "-"

const datePattern = "2006-01-02"

// UserInput - проверенные параметры запуска
type UserInput struct {
	Path        string
	Format      string
	Output      string
	From        *time.Time
	To          *time.Time
	CompareFrom *time.Time
	CompareTo   *time.Time
	Since       *time.Time
	Until       *time.Time
	Bucket      time.Duration
	Top         int
	Quiet       bool
}

// rawInput - строковые значения флагов до разбора
type rawInput struct {
	from, to, compareFrom, compareTo string
	since, until, bucket             string
}

// ParseCliFlag - разбирает аргументы командной строки и проверяет их
func ParseCliFlag(args []string, writer io.Writer) (UserInput, error) {
	fs := flag.NewFlagSet("incident-radar", flag.ContinueOnError)
	fs.SetOutput(writer)

	var in UserInput
	var raw rawInput

	fs.StringVar(&in.Path, "path", "", "log file, glob, URL or - for stdin")
	fs.StringVar(&in.Path, "p", "", "shorthand for --path")
	fs.StringVar(&in.Format, "format", FormatMarkdown, "json, markdown or adoc")
	fs.StringVar(&in.Format, "f", FormatMarkdown, "shorthand for --format")
	fs.StringVar(&in.Output, "output", "", "report path (empty = stdout)")
	fs.StringVar(&in.Output, "o", "", "shorthand for --output")

	fs.StringVar(&raw.from, "from", "", "start date YYYY-MM-DD, inclusive")
	fs.StringVar(&raw.to, "to", "", "end date YYYY-MM-DD, inclusive")
	fs.StringVar(&raw.compareFrom, "compare-from", "", "start date of the compared period YYYY-MM-DD")
	fs.StringVar(&raw.compareTo, "compare-to", "", "end date of the compared period YYYY-MM-DD")

	fs.StringVar(&raw.since, "since", "", "start time RFC3339, inclusive (2015-05-17T08:00:00Z)")
	fs.StringVar(&raw.until, "until", "", "end time RFC3339, exclusive")
	fs.StringVar(&raw.bucket, "bucket", "", "timeline bucket: 1m, 5m, 1h (empty = no timeline)")
	fs.IntVar(&in.Top, "top", 10, "size of top lists")
	fs.BoolVar(&in.Quiet, "quiet", false, "one summary warning instead of a warning per malformed line")
	fs.BoolVar(&in.Quiet, "q", false, "shorthand for --quiet")

	if err := fs.Parse(args); err != nil {
		return UserInput{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if fs.NArg() > 0 {
		return UserInput{}, fmt.Errorf("%w: unexpected arguments %q (wrap glob in quotes: --path 'logs/*.log')",
			ErrUsage, fs.Args())
	}

	if err := validate(&in, raw); err != nil {
		return UserInput{}, err
	}
	return in, nil
}

// validate - проверяет путь, формат, даты, время, bucket, top и output
func validate(in *UserInput, raw rawInput) error {
	if in.Path == "" {
		return fmt.Errorf("%w: --path is required", ErrUsage)
	}

	if in.Format != FormatJSON && in.Format != FormatMarkdown && in.Format != FormatAdoc {
		return fmt.Errorf("%w: invalid format %q (use json, markdown or adoc)", ErrUsage, in.Format)
	}

	if err := parseDates(in, raw); err != nil {
		return err
	}
	if err := parseWindow(in, raw); err != nil {
		return err
	}

	if in.Top < 1 {
		return fmt.Errorf("%w: --top must be >= 1", ErrUsage)
	}

	return validateOutput(in.Output, in.Format)
}

// parseDates - разбирает --from/--to и --compare-from/--compare-to и проверяет их порядок
func parseDates(in *UserInput, raw rawInput) error {
	var err error
	if in.From, err = parseDate("from", raw.from); err != nil {
		return err
	}
	if in.To, err = parseDate("to", raw.to); err != nil {
		return err
	}
	if in.CompareFrom, err = parseDate("compare-from", raw.compareFrom); err != nil {
		return err
	}
	if in.CompareTo, err = parseDate("compare-to", raw.compareTo); err != nil {
		return err
	}

	if err = checkOrder("from", "to", in.From, in.To); err != nil {
		return err
	}
	return checkOrder("compare-from", "compare-to", in.CompareFrom, in.CompareTo)
}

// parseWindow - разбирает --since/--until и --bucket
func parseWindow(in *UserInput, raw rawInput) error {
	var err error
	if in.Since, err = parseTime("since", raw.since); err != nil {
		return err
	}
	if in.Until, err = parseTime("until", raw.until); err != nil {
		return err
	}
	if err = checkOrder("since", "until", in.Since, in.Until); err != nil {
		return err
	}

	if raw.bucket != "" {
		in.Bucket, err = time.ParseDuration(raw.bucket)
		if err != nil {
			return fmt.Errorf("%w: invalid --bucket %q (use 1m, 5m or 1h)", ErrUsage, raw.bucket)
		}
	}
	return nil
}

// validateOutput - проверяет расширение output-файла и что файла еще нет, пустой output - stdout
func validateOutput(output, format string) error {
	if output == "" {
		return nil
	}

	wantExt := ".md"
	switch format {
	case FormatJSON:
		wantExt = ".json"
	case FormatAdoc:
		wantExt = ".adoc"
	}

	if gotExt := filepath.Ext(output); gotExt != wantExt {
		return fmt.Errorf("%w: output extension %q does not match format %q (use %q)", ErrUsage, gotExt, format, wantExt)
	}

	_, err := os.Stat(output)
	if err == nil {
		return fmt.Errorf("%w: output file %q already exists", ErrUsage, output)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: cannot check output %q: %w", ErrUsage, output, err)
	}
	return nil
}

// parseDate - разбирает дату YYYY-MM-DD, пустая строка - nil
func parseDate(name, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}

	t, err := time.Parse(datePattern, value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid --%s %q (use YYYY-MM-DD)", ErrUsage, name, value)
	}
	return &t, nil
}

// parseTime - разбирает время RFC3339, пустая строка - nil
func parseTime(name, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}

	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid --%s %q (use RFC3339, e.g. 2015-05-17T08:00:00Z)", ErrUsage, name, value)
	}
	return &t, nil
}

// checkOrder - если заданы обе границы, начало должно быть раньше конца
func checkOrder(fromName, toName string, from, to *time.Time) error {
	if from == nil || to == nil {
		return nil
	}
	if !from.Before(*to) {
		return fmt.Errorf("%w: --%s must be before --%s", ErrUsage, fromName, toName)
	}
	return nil
}
