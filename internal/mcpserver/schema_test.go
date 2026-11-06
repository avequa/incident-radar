package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avequa/incident-radar/internal/analyze"
)

// update - перегенерировать schema/report.schema.json: go test ./internal/mcpserver -run TestSchemaFile -update
var update = flag.Bool("update", false, "rewrite schema/report.schema.json")

// schemaFile - путь к файлу контракта относительно пакета
var schemaFile = filepath.Join("..", "..", "schema", "report.schema.json")

// TestSchemaFile - закоммиченный файл схемы совпадает со схемой, выведенной из Go-типов
func TestSchemaFile(t *testing.T) {
	schema, err := ReportSchema()
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')

	if *update {
		if err = os.WriteFile(schemaFile, want, 0o644); err != nil { //nolint:gosec // файл контракта читают люди и CI
			t.Fatal(err)
		}
	}

	got, err := os.ReadFile(schemaFile) //nolint:gosec // путь задан в тесте
	if err != nil {
		t.Fatalf("read %s: %v (run with -update to create it)", schemaFile, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is outdated: run go test ./internal/mcpserver -run TestSchemaFile -update", schemaFile)
	}
}

// TestSchemaValidatesReport - JSON-отчет CLI проходит валидацию по схеме, сломанный отчет - нет
func TestSchemaValidatesReport(t *testing.T) {
	schema, err := ReportSchema()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}

	day := time.Date(2015, time.May, 17, 0, 0, 0, 0, time.UTC)
	rep, err := analyze.Run(context.Background(), analyze.Options{
		Path: analyze.StdinPath, Stdin: strings.NewReader(incidentLog()),
		Bucket: time.Minute, Since: &day, CompareTo: &day,
	})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := analyze.WriteJSON(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(doc); err != nil {
		t.Fatalf("report does not match schema: %v", err)
	}

	doc["schemaVersion"] = "0.0.1"
	if err := resolved.Validate(doc); err == nil {
		t.Fatal("report with wrong schemaVersion passed validation")
	}
	doc["schemaVersion"] = analyze.SchemaVersion
	doc["responseCodes"] = map[string]any{"ok": 1}
	if err := resolved.Validate(doc); err == nil {
		t.Fatal("report with non-numeric status code passed validation")
	}
}
