package analyze

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/avequa/incident-radar/internal/source"
)

// maxRedirects - сколько редиректов разрешено при чтении URL под политикой
const maxRedirects = 5

// Policy - ограничения на источники для недоверенного вызывающего (LLM-агента); nil - без ограничений (CLI)
type Policy struct {
	Root               string
	AllowedDirs        []string
	AllowedURLPrefixes []string
	AllowStdin         bool
	MaxBytes           int64
	MaxFiles           int
}

// sourceRef - источник, готовый к открытию: имя для отчета и функция открытия
type sourceRef struct {
	name string
	open func(ctx context.Context, client *http.Client) (io.ReadCloser, error)
}

// resolveSources - раскрывает Path в список источников и проверяет их политикой
func resolveSources(opts Options) ([]sourceRef, error) {
	if opts.Path == StdinPath {
		return stdinSource(opts)
	}

	p := opts.Policy
	names, err := source.FindSources(p.resolvePath(opts.Path))
	if err != nil {
		return nil, err
	}
	if p != nil && p.MaxFiles > 0 && len(names) > p.MaxFiles {
		return nil, fmt.Errorf("%w: %d files match %q (max %d): use a narrower pattern", ErrLimit, len(names), opts.Path, p.MaxFiles)
	}

	refs := make([]sourceRef, 0, len(names))
	for _, name := range names {
		if err := p.check(name); err != nil {
			return nil, err
		}
		refs = append(refs, sourceRef{
			name: p.displayName(name),
			open: func(ctx context.Context, client *http.Client) (io.ReadCloser, error) {
				return source.OpenStream(ctx, client, name)
			},
		})
	}
	return refs, nil
}

// stdinSource - источник из Options.Stdin, если политика его разрешает
func stdinSource(opts Options) ([]sourceRef, error) {
	if opts.Policy != nil && !opts.Policy.AllowStdin {
		return nil, fmt.Errorf("%w: reading from stdin is disabled", ErrForbidden)
	}
	if opts.Stdin == nil {
		return nil, fmt.Errorf("%w: path %q needs a stdin reader", ErrInvalidOptions, StdinPath)
	}
	return []sourceRef{{
		name: "stdin",
		open: func(context.Context, *http.Client) (io.ReadCloser, error) {
			return io.NopCloser(opts.Stdin), nil
		},
	}}, nil
}

// resolvePath - относительный локальный путь считается от Root
func (p *Policy) resolvePath(s string) string {
	if p == nil || p.Root == "" || source.IsURL(s) || filepath.IsAbs(s) {
		return s
	}
	return filepath.Join(p.Root, s)
}

// displayName - путь внутри Root показывается относительно Root, остальное как есть
func (p *Policy) displayName(name string) string {
	if p == nil || p.Root == "" || source.IsURL(name) {
		return name
	}
	rel, err := filepath.Rel(p.Root, name)
	if err != nil || isOutside(rel) {
		return name
	}
	return filepath.ToSlash(rel)
}

// check - разрешен ли источник политикой
func (p *Policy) check(name string) error {
	if p == nil {
		return nil
	}
	if source.IsURL(name) {
		return p.checkURL(name)
	}
	return p.checkFile(name)
}

// checkFile - файл должен лежать внутри Root или AllowedDirs и по пути, и после раскрытия симлинков
// путь проверяется до обращения к диску, чтобы по ошибке нельзя было узнать, есть ли файл снаружи
func (p *Policy) checkFile(name string) error {
	abs, err := filepath.Abs(name)
	if err != nil {
		return fmt.Errorf("%w: invalid path %q: %w", ErrInvalidOptions, name, err)
	}
	if !p.within(abs, false) {
		return fmt.Errorf("%w: %q is outside the allowed directories", ErrForbidden, name)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("%w: open %q: %w", source.ErrSource, name, err)
	}
	if !p.within(resolved, true) {
		return fmt.Errorf("%w: %q is outside the allowed directories", ErrForbidden, name)
	}
	return nil
}

// within - лежит ли путь внутри одного из разрешенных каталогов; resolved - сравнивать с каталогами без симлинков
func (p *Policy) within(name string, resolved bool) bool {
	for _, dir := range p.dirs() {
		base, err := filepath.Abs(dir)
		if resolved {
			base, err = realPath(dir)
		}
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(base, name); err == nil && !isOutside(rel) {
			return true
		}
	}
	return false
}

// checkURL - URL должен начинаться с одного из AllowedURLPrefixes (схема, хост и префикс пути)
func (p *Policy) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: invalid URL %q: %w", source.ErrSource, raw, err)
	}
	if p.allowURL(u) {
		return nil
	}
	return fmt.Errorf("%w: URL %q does not match any allowed prefix", ErrForbidden, raw)
}

// allowURL - совпадает ли URL с одним из разрешенных префиксов
func (p *Policy) allowURL(u *url.URL) bool {
	if u.User != nil {
		return false
	}
	cleanPath := path.Clean("/" + u.Path)
	for _, prefix := range p.AllowedURLPrefixes {
		allowed, err := url.Parse(prefix)
		if err != nil {
			continue
		}
		if !strings.EqualFold(u.Scheme, allowed.Scheme) || !strings.EqualFold(u.Host, allowed.Host) {
			continue
		}
		allowedPath := path.Clean("/" + allowed.Path)
		if allowedPath == "/" || cleanPath == allowedPath || strings.HasPrefix(cleanPath, strings.TrimSuffix(allowedPath, "/")+"/") {
			return true
		}
	}
	return false
}

// dirs - все разрешенные каталоги: Root и AllowedDirs
func (p *Policy) dirs() []string {
	dirs := slices.Clone(p.AllowedDirs)
	if p.Root != "" {
		dirs = append(dirs, p.Root)
	}
	return dirs
}

// restrictClient - копия клиента, которая проверяет политикой каждый редирект
func (p *Policy) restrictClient(client *http.Client) *http.Client {
	if p == nil {
		return client
	}
	restricted := *client
	restricted.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: more than %d redirects", ErrForbidden, maxRedirects)
		}
		if !p.allowURL(req.URL) {
			return fmt.Errorf("%w: redirect to %q does not match any allowed prefix", ErrForbidden, req.URL.Redacted())
		}
		return nil
	}
	return &restricted
}

// realPath - абсолютный путь с раскрытыми симлинками
func realPath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// isOutside - выходит ли относительный путь за пределы базового каталога
func isOutside(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel)
}

// byteBudget - общий лимит прочитанных байт по всем источникам одного анализа
type byteBudget struct {
	limit int64
	read  int64
}

// newBudget - лимит из политики, nil - без лимита
func newBudget(p *Policy) *byteBudget {
	if p == nil || p.MaxBytes <= 0 {
		return nil
	}
	return &byteBudget{limit: p.MaxBytes}
}

// wrap - оборачивает поток проверкой лимита
func (b *byteBudget) wrap(r io.Reader) io.Reader {
	if b == nil {
		return r
	}
	return &budgetReader{r: r, budget: b}
}

// budgetReader - io.Reader, который возвращает ErrLimit после превышения лимита
type budgetReader struct {
	r      io.Reader
	budget *byteBudget
}

// Read - читает не больше остатка лимита плюс один байт, чтобы отличить "ровно лимит" от превышения
func (br *budgetReader) Read(p []byte) (int, error) {
	if br.budget.read > br.budget.limit {
		return 0, fmt.Errorf("%w: sources are larger than %d bytes", ErrLimit, br.budget.limit)
	}
	if remaining := br.budget.limit - br.budget.read + 1; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := br.r.Read(p)
	br.budget.read += int64(n)
	return n, err
}

// SourceInfo - локальный лог, доступный для анализа
type SourceInfo struct {
	Path      string `json:"path" jsonschema:"path to pass to analyze_logs"`
	SizeBytes int64  `json:"sizeBytes"`
	ModTime   string `json:"modTime" jsonschema:"last modification time, RFC3339 UTC"`
}

// SourceList - результат ListSources
type SourceList struct {
	Sources   []SourceInfo `json:"sources"`
	Truncated bool         `json:"truncated" jsonschema:"true if more files match than the limit"`
}

// ListSources - локальные .log/.txt файлы по glob-маске, разрешенные политикой
func ListSources(pattern string, p *Policy) (SourceList, error) {
	if p == nil {
		return SourceList{}, fmt.Errorf("%w: listing sources requires a policy", ErrInvalidOptions)
	}
	if pattern == "" {
		pattern = "*"
	}
	if source.IsURL(pattern) {
		return SourceList{}, fmt.Errorf("%w: only local files can be listed", ErrInvalidOptions)
	}

	matches, err := filepath.Glob(p.resolvePath(pattern))
	if err != nil {
		return SourceList{}, fmt.Errorf("%w: bad glob pattern %q: %w", ErrInvalidOptions, pattern, err)
	}
	slices.Sort(matches)

	limit := p.MaxFiles
	if limit <= 0 {
		limit = 1000
	}

	list := SourceList{Sources: []SourceInfo{}}
	for _, match := range matches {
		info, ok := p.sourceInfo(match)
		if !ok {
			continue
		}
		if len(list.Sources) == limit {
			list.Truncated = true
			break
		}
		list.Sources = append(list.Sources, info)
	}
	return list, nil
}

// sourceInfo - описание файла, если это разрешенный обычный .log/.txt файл
func (p *Policy) sourceInfo(name string) (SourceInfo, bool) {
	if !source.HasSupportedExt(name) || p.checkFile(name) != nil {
		return SourceInfo{}, false
	}
	stat, err := os.Stat(name)
	if err != nil || !stat.Mode().IsRegular() {
		return SourceInfo{}, false
	}
	return SourceInfo{
		Path:      p.displayName(name),
		SizeBytes: stat.Size(),
		ModTime:   formatTime(stat.ModTime()),
	}, true
}

// IsUsageError - относится ли ошибка движка к ожидаемым (ошибка ввода, источника, политики или лимита)
func IsUsageError(err error) bool {
	return errors.Is(err, ErrInvalidOptions) || errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrLimit) || errors.Is(err, source.ErrSource)
}
