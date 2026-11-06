package analyze

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/avequa/incident-radar/internal/parser"
	"github.com/avequa/incident-radar/internal/report"
	"github.com/avequa/incident-radar/internal/source"
	"github.com/avequa/incident-radar/internal/stats"
)

// warnBadLine - префикс предупреждения о битой строке
const warnBadLine = "WARN: skipped malformed log line"

// ctxCheckEvery - как часто (в строках) проверять отмену context
const ctxCheckEvery = 1024

// lineCounters - счетчики строк по всем источникам
type lineCounters struct {
	processed int
	malformed int
}

// engine - состояние одного прохода: все сборщики статистики
type engine struct {
	opts      Options
	mainDates stats.Period
	compare   *stats.Period
	main      *stats.HandlerLogLine
	compared  *stats.HandlerLogLine
	incident  *incidentCollector
	timeline  *timelineCollector
	first     time.Time
	last      time.Time
	lines     lineCounters
}

// newEngine - создает сборщики под опции
func newEngine(opts Options) *engine {
	e := &engine{
		opts:      opts,
		mainDates: stats.Period{From: opts.From, To: opts.To},
		main:      stats.NewHandlerLogLine(),
		incident:  newIncidentCollector(),
	}
	if opts.CompareFrom != nil || opts.CompareTo != nil {
		e.compare = &stats.Period{From: opts.CompareFrom, To: opts.CompareTo}
		e.compared = stats.NewHandlerLogLine()
	}
	if opts.Bucket > 0 {
		e.timeline = newTimelineCollector(opts.Bucket)
	}
	return e
}

// readSource - открывает источник, читает его построчно и закрывает
func (e *engine) readSource(ctx context.Context, client *http.Client, budget *byteBudget, src sourceRef) error {
	stream, err := src.open(ctx, client)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	if err := e.readLines(ctx, budget.wrap(stream), src.name); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("read %q: %w", src.name, err)
		}
		return fmt.Errorf("%w: read %q: %w", source.ErrSource, src.name, err)
	}
	return nil
}

// readLines - читает поток по строкам, битые и слишком длинные строки пропускает с предупреждением
func (e *engine) readLines(ctx context.Context, r io.Reader, name string) error {
	reader := bufio.NewReaderSize(r, 64*1024)
	var buf []byte

	for lineNo := 1; ; lineNo++ {
		if lineNo%ctxCheckEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}

		line, tooLong, err := readLine(reader, MaxLineBytes, buf[:0])
		if errors.Is(err, io.EOF) {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		buf = line

		if !tooLong && len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		e.lines.processed++

		if tooLong {
			e.malformed(name, lineNo, "line is longer than 1 MiB")
			continue
		}

		record, err := parser.ParseLine(string(line))
		if err != nil {
			e.malformed(name, lineNo, "")
			continue
		}
		e.add(record)
	}
}

// malformed - учитывает битую строку и, если не включен quiet, пишет предупреждение
func (e *engine) malformed(name string, lineNo int, reason string) {
	e.lines.malformed++
	if e.opts.QuietWarnings {
		return
	}
	if reason != "" {
		reason = ", " + reason
	}
	_, _ = fmt.Fprintf(e.opts.Warnings, "%s (%s:%d%s)\n", warnBadLine, name, lineNo, reason)
}

// add - раскладывает запись по сборщикам: основной период, окно, сравнение
func (e *engine) add(rec parser.LogRecord) {
	if e.first.IsZero() || rec.Time.Before(e.first) {
		e.first = rec.Time
	}
	if rec.Time.After(e.last) {
		e.last = rec.Time
	}

	if e.inMain(rec.Time) {
		e.main.Add(rec)
		e.incident.add(rec)
		if e.timeline != nil {
			e.timeline.add(rec)
		}
	}
	if e.compare != nil && e.compare.FilterPeriodEveryLine(rec.Time) {
		e.compared.Add(rec)
	}
}

// inMain - попадает ли время в основной период по датам и в окно since/until
func (e *engine) inMain(t time.Time) bool {
	if !e.mainDates.FilterPeriodEveryLine(t) {
		return false
	}
	if e.opts.Since != nil && t.Before(*e.opts.Since) {
		return false
	}
	if e.opts.Until != nil && !t.Before(*e.opts.Until) {
		return false
	}
	return true
}

// report - собирает итоговый отчет после чтения всех источников
func (e *engine) report(sources []sourceRef) (Report, error) {
	files := make([]string, 0, len(sources))
	for _, src := range sources {
		files = append(files, src.name)
	}

	rep := Report{
		SchemaVersion: SchemaVersion,
		Report: report.Report{
			Files:               files,
			FilesCount:          len(files),
			ProcessedLinesCount: e.lines.processed,
			MalformedLinesCount: e.lines.malformed,
			Result:              e.main.ResultTop(e.opts.Top),
		},
	}
	if e.compare != nil {
		rep.Comparison = report.NewComparison(e.mainDates, *e.compare, rep.Result, e.compared.ResultTop(e.opts.Top))
	}
	if !e.first.IsZero() {
		rep.TimeRange = &TimeRange{First: formatTime(e.first), Last: formatTime(e.last)}
	}
	if e.opts.Since != nil || e.opts.Until != nil {
		rep.Window = &Window{Since: formatTimePtr(e.opts.Since), Until: formatTimePtr(e.opts.Until)}
	}

	e.incident.fill(&rep, e.opts.Top)

	if e.timeline != nil {
		timeline, err := e.timeline.build(e.opts.MaxTimelinePoints)
		if err != nil {
			return Report{}, err
		}
		rep.Timeline = timeline
	}
	return rep, nil
}

// readLine - читает строку без \n и \r\n; строку длиннее limit дочитывает, выбрасывает и возвращает tooLong
// в памяти никогда не лежит больше limit плюс размер буфера reader, io.EOF - только когда данных больше нет
func readLine(reader *bufio.Reader, limit int, buf []byte) (line []byte, tooLong bool, err error) {
	for {
		chunk, readErr := reader.ReadSlice('\n')
		if !tooLong {
			buf = append(buf, chunk...)
			if len(buf) > limit+2 {
				tooLong = true
				buf = buf[:0]
			}
		}

		switch {
		case readErr == nil:
			return finishLine(buf, limit, tooLong)
		case errors.Is(readErr, bufio.ErrBufferFull):
			continue
		case errors.Is(readErr, io.EOF):
			if len(buf) == 0 && !tooLong {
				return nil, false, io.EOF
			}
			return finishLine(buf, limit, tooLong)
		default:
			return nil, false, readErr
		}
	}
}

// finishLine - убирает \n или \r\n и окончательно проверяет длину строки
func finishLine(buf []byte, limit int, tooLong bool) ([]byte, bool, error) {
	if tooLong {
		return buf[:0], true, nil
	}
	buf = bytes.TrimSuffix(buf, []byte("\n"))
	buf = bytes.TrimSuffix(buf, []byte("\r"))
	if len(buf) > limit {
		return buf[:0], true, nil
	}
	return buf, false, nil
}

// formatTime - время в RFC3339 UTC
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// formatTimePtr - время в RFC3339 UTC, nil для незаданной границы
func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}
