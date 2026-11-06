// Package parser разбирает строку NGINX-лога в формате combined в LogRecord
package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrBadLine - строка не соответствует формату NGINX combined
var ErrBadLine = errors.New("malformed log line")

// LogRecord - одна разобранная строка лога
type LogRecord struct {
	IP       string
	Time     time.Time
	Method   string
	Resource string
	Protocol string
	Status   int
	Size     int
}

// timeLayout - формат $time_local, например 17/May/2015:08:05:32 +0000
const timeLayout = "02/Jan/2006:15:04:05 -0700"

// lineRe - формат строки: ip - user [time] "request" status size "referer" "user agent"
var lineRe = regexp.MustCompile(
	`^(\S+) - (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\d+) "([^"]*)" "([^"]*)"$`,
)

// ParseLine - разбирает строку лога в LogRecord, для битой строки возвращает ErrBadLine
func ParseLine(line string) (LogRecord, error) {
	parts := lineRe.FindStringSubmatch(line)
	if parts == nil {
		return LogRecord{}, fmt.Errorf("%w: line does not match the combined format", ErrBadLine)
	}

	t, err := time.Parse(timeLayout, parts[3])
	if err != nil {
		return LogRecord{}, fmt.Errorf("%w: invalid time %q: %w", ErrBadLine, parts[3], err)
	}

	request := strings.Fields(parts[4])
	if len(request) != 3 {
		return LogRecord{}, fmt.Errorf("%w: request %q must be \"METHOD RESOURCE PROTOCOL\"", ErrBadLine, parts[4])
	}

	status, err := strconv.Atoi(parts[5])
	if err != nil || status < 100 || status > 599 {
		return LogRecord{}, fmt.Errorf("%w: invalid status %q", ErrBadLine, parts[5])
	}

	size, err := strconv.Atoi(parts[6])
	if err != nil {
		return LogRecord{}, fmt.Errorf("%w: invalid size %q", ErrBadLine, parts[6])
	}

	return LogRecord{
		IP:       parts[1],
		Time:     t,
		Method:   request[0],
		Resource: request[1],
		Protocol: request[2],
		Status:   status,
		Size:     size,
	}, nil
}
