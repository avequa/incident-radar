package mcpserver

import (
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/avequa/incident-radar/internal/analyze"
)

// schemaDialect - версия JSON Schema для файла контракта
const schemaDialect = "https://json-schema.org/draft/2020-12/schema"

// ReportSchema - JSON Schema отчета analyze.Report, из нее же генерируется schema/report.schema.json
func ReportSchema() (*jsonschema.Schema, error) {
	statusCodes := &jsonschema.Schema{
		Type:                 "object",
		PropertyNames:        &jsonschema.Schema{Type: "string", Pattern: "^[1-5][0-9]{2}$"},
		AdditionalProperties: &jsonschema.Schema{Type: "integer"},
	}

	schema, err := jsonschema.For[analyze.Report](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[map[int]int](): statusCodes},
	})
	if err != nil {
		return nil, err
	}

	schema.Schema = schemaDialect
	schema.ID = "https://github.com/avequa/incident-radar/schema/report.schema.json"
	schema.Title = "incident-radar report"
	schema.Description = "NGINX access log statistics produced by incident-radar, schemaVersion " + analyze.SchemaVersion
	schema.Properties["schemaVersion"].Const = jsonschema.Ptr[any](analyze.SchemaVersion)
	return schema, nil
}

// analyzeInputSchema - схема входа analyze_logs с перечислением шагов и границами top, понятными малым моделям
func analyzeInputSchema() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[AnalyzeInput](nil)
	if err != nil {
		return nil, err
	}
	schema.Properties["bucket"].Enum = []any{"1m", "5m", "15m", "1h"}
	schema.Properties["top"].Minimum = new(1.0)
	schema.Properties["top"].Maximum = new(float64(maxTop))
	schema.Properties["from"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	schema.Properties["to"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	schema.Properties["compareFrom"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	schema.Properties["compareTo"].Pattern = `^\d{4}-\d{2}-\d{2}$`
	return schema, nil
}
