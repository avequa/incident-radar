// Package output сохраняет отчет в новый файл и никогда не перезаписывает существующий
package output

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrExists - output-файл уже существует
var ErrExists = errors.New("output file already exists")

// WriteNew - создает файл path (и недостающие папки) и записывает в него data, существующий файл не трогает
func WriteNew(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // папка для отчета, который читают люди
		return fmt.Errorf("create output directory for %q: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // путь и права отчета задает пользователь CLI
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %q", ErrExists, path)
	}
	if err != nil {
		return fmt.Errorf("open output %q: %w", path, err)
	}

	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write output %q: %w", path, err)
	}

	if err = file.Close(); err != nil {
		return fmt.Errorf("close output %q: %w", path, err)
	}
	return nil
}
