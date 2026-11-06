# Команды сборки, запуска и проверки проекта
GO ?= go
ARGS ?=
PORT ?= 8000
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: install build run serve mcp mcp-http docker format check test test-race schema

install: ## Скачать зависимости и локальный линтер
	$(GO) mod download

build: ## Собрать CLI и MCP-сервер в bin/
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/incident-radar ./cmd/incident-radar-mcp

run: ## Запустить CLI, параметры передаются через ARGS
	$(GO) run ./cmd/incident-radar $(ARGS)

serve: ## Запустить локальный HTTP-сервер с логами из sample_logs
	$(GO) run ./cmd/log-server --port $(PORT)

mcp: ## Запустить MCP-сервер по stdio с логами из sample_logs
	$(GO) run ./cmd/incident-radar-mcp --root sample_logs $(ARGS)

mcp-http: ## Запустить MCP-сервер по streamable HTTP на 127.0.0.1:8765
	$(GO) run ./cmd/incident-radar-mcp --transport http --root sample_logs --log-format text $(ARGS)

docker: ## Собрать Docker-образ incident-radar:$(VERSION)
	docker build --build-arg VERSION=$(VERSION) -t incident-radar:$(VERSION) .

format: ## Применить gofumpt и goimports
	$(GO) tool golangci-lint fmt

check: ## Проверить go.mod, форматирование и код линтерами
	$(GO) mod tidy -diff
	$(GO) tool golangci-lint config verify
	$(GO) tool golangci-lint fmt --diff
	$(GO) tool golangci-lint run

test: ## Запустить тесты и сформировать HTML-отчет покрытия
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	$(GO) tool cover -html=coverage.out -o coverage.html

test-race: ## Запустить тесты с race detector (нужен CGO)
	$(GO) test -race ./...

schema: ## Перегенерировать schema/report.schema.json из Go-типов
	$(GO) test ./internal/mcpserver -run TestSchemaFile -update
