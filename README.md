# incident-radar

Утилита на Go для разбора access-логов NGINX. Она за один проход читает логи из файла, по маске, по URL или из stdin и считает всё, что нужно при разборе инцидента: коды ответов, долю 5xx, самые частые URL и IP, URL с ошибками, размеры ответов и разбивку по минутам. По этой разбивке сразу видно, в какую минуту всё сломалось.

У утилиты два режима: обычный CLI для человека и MCP-сервер, через который её вызывают LLM-агенты как tool.

## Место в проекте

Это основа проекта Incident Copilot: все данные о логах агенты получают только отсюда.

| Репозиторий | Язык | Роль |
|---|---|---|
| **incident-radar** (этот) | Go | читает логи и считает статистику, для агентов это MCP-сервер с tools |
| [llm-gateway](https://github.com/avequa/llm-gateway) | Go | шлюз к моделям |
| [incident-copilot](https://github.com/avequa/incident-copilot) | Python | агенты на LangGraph |

Модели нельзя отдавать сырые логи: они огромные и не влезают в контекст, а цифры модель легко перепутает. Поэтому считает радар, а модель получает короткую сводку в JSON и только объясняет её. Агенту мы не доверяем, его можно обмануть текстом прямо в логе. Поэтому все ограничения (какие папки читать, сколько байт, сколько времени) проверяет сервер, а не модель.

## Что внутри

- два бинарника: `incident-radar` (CLI, только стандартная библиотека Go) и `incident-radar-mcp` (MCP-сервер на официальном [Go SDK](https://github.com/modelcontextprotocol/go-sdk))
- CLI и MCP вызывают одну и ту же функцию `analyze.Run`, поэтому цифры у них всегда одинаковые
- отчёт в JSON, Markdown или AsciiDoc, формат JSON закреплён в [JSON Schema](schema/report.schema.json)
- MCP по stdio и по streamable HTTP с Bearer-токеном
- защита от недоверенного вызова: список разрешённых папок и URL, лимиты на размер, число файлов и время, проверка `../` и симлинков
- тесты покрывают около 93% кода, в CI линтеры (golangci-lint с gosec), тесты с `-race`, сборка бинарников и Docker-образа
- Docker-образ около 30 МБ на distroless, запускается не от root

```text
                    ┌──────────────────┐
  флаги CLI ───────►│ incident-radar   │──► JSON / Markdown / AsciiDoc
                    └────────┬─────────┘
                             │  analyze.Run(ctx, Options) -> Report
                    ┌────────▼─────────┐     source ─► parser ─► stats + агрегаты инцидента
  вызов tool ──────►│ incident-radar-  │──► structuredContent (JSON Schema)
  (MCP, LangGraph)  │ mcp + Policy     │
                    └──────────────────┘
```

## Запуск

Нужен Go 1.26+.

```bash
make build    # bin/incident-radar и bin/incident-radar-mcp
make test     # тесты и coverage.html
```

## CLI

Найти минуту начала инцидента в демо-логе, где с 08:17 по Москве сыплются 5xx на `/api/checkout`:

```bash
./bin/incident-radar -p sample_logs/incident.log -q --bucket 1m --top 3 \
  --since 2026-03-14T08:10:00+03:00 --until 2026-03-14T08:25:00+03:00
```

```markdown
## Временной ряд (шаг 1m)

| Начало | Запросы | 2xx | 3xx | 4xx | 5xx | errorRate | p95 размера |
|:---|---:|---:|---:|---:|---:|---:|---:|
| 2026-03-14T05:16:00Z | 16 | 13 | 2 | 1 | 0 | 0 | 35674 |
| 2026-03-14T05:17:00Z | 26 | 11 | 2 | 0 | 13 | 0.5 | 34775 |
| 2026-03-14T05:18:00Z | 24 | 11 | 1 | 1 | 11 | 0.4583 | 29603 |
```

Ещё примеры:

```bash
./bin/incident-radar --path 'logs/*.log' --format json --output reports/today.json --from 2025-03-01
cat access.log | ./bin/incident-radar --path - -f json --quiet --bucket 5m | jq .errorRate
```

| Параметр | Что значит |
|---|---|
| `--path`, `-p` | файл, маска в кавычках, URL или `-` для stdin, обязательный |
| `--format`, `-f` | `json`, `markdown` или `adoc`, по умолчанию `markdown` |
| `--output`, `-o` | куда записать отчёт, без него печатается в stdout |
| `--from`, `--to` | период по датам `YYYY-MM-DD`, обе границы включительно |
| `--compare-from`, `--compare-to` | второй период для сравнения с основным |
| `--since`, `--until` | окно по времени в RFC3339, `until` не включительно |
| `--bucket` | шаг разбивки: `1m`, `5m`, `1h` и т.п. |
| `--top` | размер топов, по умолчанию 10 |
| `--quiet`, `-q` | одна сводка про битые строки в конце вместо предупреждения на каждую |

Отчёт идёт в stdout, предупреждения и ошибки в stderr, поэтому вывод можно сразу отдавать в `jq`. Существующий файл отчёта не перезаписывается. Битая строка лога пропускается с предупреждением, чтение продолжается. Коды выхода: `0` успех, `1` внутренняя ошибка или прерывание, `2` ошибка в аргументах или источнике.

Полные примеры отчётов лежат в [examples/](examples/).

## MCP-сервер

```bash
./bin/incident-radar-mcp --root /var/log/nginx                     # stdio: клиент сам запускает сервер
INCIDENT_RADAR_MCP_TOKEN=secret ./bin/incident-radar-mcp \
  --transport http --addr 127.0.0.1:8765 --root /var/log/nginx     # streamable HTTP на /mcp
```

Tools:

- **`list_sources`** `{glob?}`: файлы `.log` и `.txt` в разрешённых папках с размером и датой изменения
- **`analyze_logs`** `{path, from?, to?, since?, until?, bucket?, compareFrom?, compareTo?, top?}`: сводка по JSON Schema отчёта. `bucket` ограничен значениями `1m|5m|15m|1h`, а `top` диапазоном `1..100`, чтобы маленьким моделям было проще вызывать tool правильно

Если агент передал что-то не то, сервер возвращает ошибку tool с понятным текстом, например `timeline would have 1440 points (max 300): use a larger bucket or a narrower since/until window`. Модель читает её и сама исправляет аргументы.

Подключение из Python (LangGraph):

```python
from langchain_mcp_adapters.client import MultiServerMCPClient

client = MultiServerMCPClient({
    "incident-radar": {
        "transport": "streamable_http",
        "url": "http://127.0.0.1:8765/mcp",
        "headers": {"Authorization": "Bearer secret"},
    },
    # или по stdio:
    # "incident-radar": {"transport": "stdio", "command": "./bin/incident-radar-mcp", "args": ["--root", "logs"]},
})
tools = await client.get_tools()   # list_sources, analyze_logs
```

Проверено с `langchain-mcp-adapters` 0.3.2 и Python-пакетом `mcp` 1.30 по обоим транспортам. С `mcp` 2.3 клиент по stdio зависает на получении списка tools, поэтому в incident-copilot версия закреплена `mcp>=1.30,<2`.

### Ограничения для агента

| Флаг | По умолчанию | Что ограничивает |
|---|---|---|
| `--root` | `.` | папка с логами, относительные пути считаются от неё |
| `--allow-dir` | нет | дополнительные папки, флаг можно повторять |
| `--allow-url` | нет | разрешённые префиксы URL, без флага URL запрещены |
| `--max-bytes` | 512 MiB | сколько можно прочитать за один вызов |
| `--max-files` | 100 | сколько файлов может найти маска |
| `--call-timeout` | 60s | время одного вызова |
| `--max-concurrent` | 4 | сколько вызовов одновременно |
| `--max-timeline-points` | 300 | длина разбивки по времени, чтобы ответ влез в контекст модели |
| `INCIDENT_RADAR_MCP_TOKEN` | нет | Bearer-токен для `/mcp` |

Путь проверяется дважды: до обращения к диску и после раскрытия симлинков. Каждый редирект URL снова сверяется со списком разрешённых. Логи сервера пишутся через `slog` в stderr: каждый вызов tool с длительностью и ошибкой.

### Docker

```bash
make docker VERSION=0.1.0
docker run --rm -p 8765:8765 -e INCIDENT_RADAR_MCP_TOKEN=secret \
  -v /var/log/nginx:/logs:ro --read-only --cap-drop ALL incident-radar:0.1.0
```

## Как считается

- один проход по логу: каждая запись сразу попадает во все подсчёты, второе чтение не нужно
- строки не копятся в памяти, буфер строки ограничен 1 MiB
- разбивка по времени без пропусков: минута без запросов тоже попадает в отчёт с нулями, ведь ноль трафика тоже признак инцидента
- всё время в отчёте в UTC, топы отсортированы по количеству, при равенстве по имени, поэтому результат всегда одинаковый
- p95 считается по размеру ответа: в формате combined нет времени ответа

## Структура

```text
cmd/
  incident-radar/       CLI
  incident-radar-mcp/   MCP-сервер
  log-server/           локальный HTTP-сервер с sample_logs для проверки чтения по URL
internal/
  source/     поиск источников (файл, маска, URL) и открытие потока
  parser/     разбор строки формата combined
  stats/      базовая статистика
  analyze/    движок: окно по времени, разбивка, агрегаты инцидента, ограничения для агента
  report/     рендер JSON, Markdown, AsciiDoc
  mcpserver/  tools, JSON Schema, транспорты, токен, корректная остановка
  cli/, app/, output/   флаги, запуск CLI, запись отчёта
schema/       report.schema.json
sample_logs/  демо-логи, в incident.log инцидент в 08:17 по Москве
```

## Ограничения

- только формат NGINX `combined`
- маска без рекурсивного `**`
- для очень больших логов p95 лучше считать гистограммой или t-digest, сейчас размеры ответов хранятся в памяти
