// Package source находит источники логов (файл, glob, URL) и открывает их как поток
package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrSource - ожидаемая ошибка источника: нет файла, пустой glob, недоступный URL
var ErrSource = errors.New("source error")

// IsURL - является ли путь http(s)-адресом
func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// IsGlob - содержит ли путь символы glob-маски
func IsGlob(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// HasSupportedExt - имеет ли файл расширение .log или .txt
func HasSupportedExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".log" || ext == ".txt"
}

// FindSources - превращает --path в отсортированный список источников и проверяет их расширения
func FindSources(s string) ([]string, error) {
	if IsURL(s) {
		u, err := url.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid URL %q: %w", ErrSource, s, err)
		}
		if !HasSupportedExt(u.Path) {
			return nil, fmt.Errorf("%w: unsupported extension in %q (use .log or .txt)", ErrSource, u.Path)
		}
		return []string{s}, nil
	}

	if IsGlob(s) {
		matches, err := filepath.Glob(s)
		if err != nil {
			return nil, fmt.Errorf("%w: bad glob pattern %q: %w", ErrSource, s, err)
		}

		var files []string
		for _, match := range matches {
			if HasSupportedExt(match) {
				files = append(files, match)
			}
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("%w: no .log or .txt files match %q", ErrSource, s)
		}

		sort.Strings(files)
		return files, nil
	}

	if !HasSupportedExt(s) {
		return nil, fmt.Errorf("%w: unsupported extension in %q (use .log or .txt)", ErrSource, s)
	}
	return []string{s}, nil
}

// OpenStream - открывает файл или URL для построчного чтения, поток нужно закрыть
func OpenStream(ctx context.Context, client *http.Client, name string) (io.ReadCloser, error) {
	if IsURL(name) {
		return openURL(ctx, client, name)
	}
	return openFile(name)
}

// openURL - выполняет GET-запрос и возвращает тело успешного ответа
func openURL(ctx context.Context, client *http.Client, name string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, name, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request %q: %w", ErrSource, name, err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request %q: %w", ErrSource, name, err)
	}

	if response.StatusCode < 200 || response.StatusCode > 299 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: %q returned HTTP %d", ErrSource, name, response.StatusCode)
	}
	return response.Body, nil
}

// openFile - открывает локальный файл
func openFile(name string) (io.ReadCloser, error) {
	file, err := os.Open(name) //nolint:gosec // путь задает пользователь CLI или проверяет политика MCP-сервера
	if err != nil {
		return nil, fmt.Errorf("%w: open %q: %w", ErrSource, name, err)
	}
	return file, nil
}
