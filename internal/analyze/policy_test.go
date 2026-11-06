package analyze

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avequa/incident-radar/internal/source"
)

// TestPolicy_Files - файлы внутри Root и AllowedDirs разрешены, снаружи и через симлинк наружу запрещены
func TestPolicy_Files(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()

	writeFile(t, root, "access.log", testLine+"\n")
	writeFile(t, root, "nested/app.log", testLine+"\n")
	extraLog := writeFile(t, extra, "extra.log", testLine+"\n")
	secret := writeFile(t, outside, "secret.log", testLine+"\n")
	if err := os.Symlink(secret, filepath.Join(root, "link.log")); err != nil {
		t.Skipf("symlinks are not supported: %v", err)
	}

	policy := &Policy{Root: root, AllowedDirs: []string{extra}}

	tests := []struct {
		name    string
		path    string
		wantErr error
		files   []string
	}{
		{"relative path inside root", "access.log", nil, []string{"access.log"}},
		{"nested relative path", "nested/app.log", nil, []string{"nested/app.log"}},
		{"glob matching a symlink outside", "*.log", ErrForbidden, nil},
		{"absolute path in allowed dir", extraLog, nil, []string{extraLog}},
		{"absolute path outside", secret, ErrForbidden, nil},
		{"missing file outside is forbidden, not reported", filepath.Join(outside, "nope.log"), ErrForbidden, nil},
		{"dot-dot escape", "../" + filepath.Base(outside) + "/secret.log", ErrForbidden, nil},
		{"symlink pointing outside", "link.log", ErrForbidden, nil},
		{"missing file", "nope.log", source.ErrSource, nil},
		{"url without prefixes", "http://127.0.0.1/access.log", ErrForbidden, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := Run(context.Background(), Options{Path: tt.path, Policy: policy})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equal(rep.Files, tt.files) {
				t.Fatalf("Files = %v, want %v", rep.Files, tt.files)
			}
		})
	}
}

// TestPolicy_Stdin - stdin под политикой запрещен, если AllowStdin не включен
func TestPolicy_Stdin(t *testing.T) {
	_, err := Run(context.Background(), Options{Path: StdinPath, Stdin: strings.NewReader(testLine), Policy: &Policy{}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}

	rep, err := Run(context.Background(), Options{Path: StdinPath, Stdin: strings.NewReader(testLine), Policy: &Policy{AllowStdin: true}})
	if err != nil || rep.TotalRequestsCount != 1 {
		t.Fatalf("err = %v, total = %d, want 1 request", err, rep.TotalRequestsCount)
	}
}

// TestPolicy_MaxBytes - ровно MaxBytes читается, на байт больше - ErrLimit по сумме всех файлов
func TestPolicy_MaxBytes(t *testing.T) {
	root := t.TempDir()
	content := testLine + "\n"
	writeFile(t, root, "a.log", content)
	writeFile(t, root, "b.log", content)
	size := int64(len(content))

	tests := []struct {
		name     string
		path     string
		maxBytes int64
		wantErr  bool
	}{
		{"exactly the limit", "a.log", size, false},
		{"one byte over", "a.log", size - 1, true},
		{"sum of files over", "*.log", 2*size - 1, true},
		{"sum of files fits", "*.log", 2 * size, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(context.Background(), Options{Path: tt.path, Policy: &Policy{Root: root, MaxBytes: tt.maxBytes}})
			if tt.wantErr != errors.Is(err, ErrLimit) {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestPolicy_MaxFiles - glob, который находит больше MaxFiles файлов, возвращает ErrLimit
func TestPolicy_MaxFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.log", "b.log", "c.log"} {
		writeFile(t, root, name, testLine+"\n")
	}

	_, err := Run(context.Background(), Options{Path: "*.log", Policy: &Policy{Root: root, MaxFiles: 2}})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
}

// TestPolicy_URL - URL читается только по разрешенному префиксу, редирект наружу запрещен
func TestPolicy_URL(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, testLine+"\n")
	}))
	defer other.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/logs/access.log":
			_, _ = io.WriteString(w, testLine+"\n")
		case "/logs/redirect-inside.log":
			http.Redirect(w, r, "/logs/access.log", http.StatusFound)
		case "/logs/redirect-outside.log":
			http.Redirect(w, r, other.URL+"/access.log", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	policy := &Policy{AllowedURLPrefixes: []string{server.URL + "/logs/"}}

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"allowed prefix", server.URL + "/logs/access.log", nil},
		{"redirect inside prefix", server.URL + "/logs/redirect-inside.log", nil},
		{"redirect outside prefix", server.URL + "/logs/redirect-outside.log", ErrForbidden},
		{"other path on same host", server.URL + "/admin/access.log", ErrForbidden},
		{"dot-dot in path", server.URL + "/logs/../admin/access.log", ErrForbidden},
		{"prefix without slash boundary", server.URL + "/logs-private/access.log", ErrForbidden},
		{"other host", other.URL + "/logs/access.log", ErrForbidden},
		{"userinfo", strings.Replace(server.URL, "http://", "http://user:pass@", 1) + "/logs/access.log", ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, err := Run(context.Background(), Options{Path: tt.path, Policy: policy, HTTPClient: server.Client()})
			if tt.wantErr == nil {
				if err != nil || rep.TotalRequestsCount != 1 {
					t.Fatalf("err = %v, total = %d, want 1 request", err, rep.TotalRequestsCount)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// TestListSources - только разрешенные .log/.txt файлы, пути относительно Root, лимит с флагом truncated
func TestListSources(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "b.log", testLine+"\n")
	writeFile(t, root, "a.txt", "x")
	writeFile(t, root, "notes.md", "not a log")
	writeFile(t, root, "nested/c.log", "")
	if err := os.Mkdir(filepath.Join(root, "dir.log"), 0o750); err != nil {
		t.Fatal(err)
	}
	secret := writeFile(t, t.TempDir(), "secret.log", testLine+"\n")

	list, err := ListSources("", &Policy{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sources) != 2 || list.Sources[0].Path != "a.txt" || list.Sources[1].Path != "b.log" || list.Truncated {
		t.Fatalf("list = %+v", list)
	}
	if list.Sources[1].SizeBytes != int64(len(testLine)+1) || list.Sources[1].ModTime == "" {
		t.Fatalf("source info = %+v", list.Sources[1])
	}

	nested, err := ListSources("nested/*.log", &Policy{Root: root})
	if err != nil || len(nested.Sources) != 1 || nested.Sources[0].Path != "nested/c.log" {
		t.Fatalf("nested = %+v, err = %v", nested, err)
	}

	limited, err := ListSources("*", &Policy{Root: root, MaxFiles: 1})
	if err != nil || len(limited.Sources) != 1 || !limited.Truncated {
		t.Fatalf("limited = %+v, err = %v", limited, err)
	}

	outside, err := ListSources(secret, &Policy{Root: root})
	if err != nil || len(outside.Sources) != 0 {
		t.Fatalf("outside = %+v, err = %v", outside, err)
	}

	for _, bad := range []string{"http://example.com/*.log", "[.log"} {
		if _, err := ListSources(bad, &Policy{Root: root}); !errors.Is(err, ErrInvalidOptions) {
			t.Fatalf("ListSources(%q) err = %v, want ErrInvalidOptions", bad, err)
		}
	}
	if _, err := ListSources("*", nil); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("nil policy err = %v, want ErrInvalidOptions", err)
	}
}
