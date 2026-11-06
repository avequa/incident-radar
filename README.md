# Incident Radar

Инструмент на Go для разбора NGINX access-логов, который можно дать LLM-агенту как tool.
Он читает логи из файла, glob-маски, URL или stdin за один потоковый проход и считает статистику:
коды и классы ответов, долю 5xx, top ресурсов и IP, ресурсы с ошибками, размеры ответов (average/max/p95)
и временной ряд без пропусков. По этому ряду видно, в какую минуту начался инцидент.

Есть два бинарника:

- `incident-radar` — CLI. Пишет отчет в JSON, Markdown или AsciiDoc и использует только стандартную библиотеку Go.
- `incident-radar-mcp` — MCP-сервер на официальном [Go SDK](https://github.com/modelcontextprotocol/go-sdk)
  с tools `analyze_logs` и `list_sources`, транспортами stdio и streamable HTTP и ограничениями
  для недоверенного вызывающего: allowlist каталогов и URL, лимиты, таймауты, токен.

```text
                    ┌──────────────────┐
  CLI flags ───────►│ incident-radar   │──► JSON / Markdown / AsciiDoc
                    └────────┬─────────┘
                             │  analyze.Run(ctx, Options) -> Report
                    ┌────────▼─────────┐     source ─► parser ─► stats + агрегаты инцидента
  MCP tool call ───►│ incident-radar-  │──► structuredContent (JSON Schema v1)
  (LangGraph и др.) │ mcp + Policy     │
                    └──────────────────┘
```

## Быстрый старт

Нужен Go 1.26+.

```bash
make build                                   # bin/incident-radar и bin/incident-radar-mcp
./bin/incident-radar -p sample_logs/incident.log -q --bucket 5m
```

Найти минуту начала инцидента в демо-логе `sample_logs/incident.log`, где с 08:17 по Москве сыплются 5xx на `/api/checkout`:

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

Полные отчеты лежат в [examples/](examples/): [incident-onset.md](examples/incident-onset.md),
[incident-overview.json](examples/incident-overview.json), [report.json](examples/report.json),
[report.md](examples/report.md), [report.adoc](examples/report.adoc), [comparison.md](examples/comparison.md).

## CLI

```bash
./bin/incident-radar --path 'logs/*.log' --format json --output reports/today.json --from 2025-03-01
cat access.log | ./bin/incident-radar --path - -f json --quiet --bucket 5m
```

| Параметр         | Описание                                                                     |
| ---------------- | ---------------------------------------------------------------------------- |
| `--path`, `-p`   | Файл, glob-маска (в кавычках), URL или `-` для stdin. Обязательный           |
| `--format`, `-f` | `json`, `markdown` или `adoc`. По умолчанию `markdown`                       |
| `--output`, `-o` | Путь к отчету. Без него отчет печатается в stdout                            |
| `--from`, `--to` | Период по датам `YYYY-MM-DD`, обе границы включительно                       |
| `--compare-from`, `--compare-to` | Второй период по датам для сравнения с основным              |
| `--since`        | Начало окна по времени, RFC3339, включительно: `2026-03-14T08:10:00+03:00`   |
| `--until`        | Конец окна по времени, RFC3339, **не** включительно                          |
| `--bucket`       | Шаг временного ряда: `1m`, `5m`, `1h` (любая длительность от `1s` до `24h`)  |
| `--top`          | Размер top-списков: ресурсы, IP, ресурсы с 5xx. По умолчанию `10`            |
| `--quiet`, `-q`  | Вместо WARN на каждую битую строку одна сводка в конце                       |

Правила:

- расширение отчета должно совпадать с форматом: `.json`, `.md`, `.adoc`
- при двух границах начало должно быть раньше конца: `--from < --to`, `--compare-from < --compare-to`, `--since < --until`
- существующий отчет не перезаписывается: файл создается через `O_CREATE|O_EXCL`, недостающие папки создаются
- отчет пишется только после того, как все источники прочитаны
- отчет идет в stdout, предупреждения и ошибки идут в stderr, поэтому `incident-radar ... -f json | jq` работает
- `--from/--to` фильтруют по дню записи так, как он записан в логе; `--since/--until` фильтруют по моменту времени
  и учитывают часовой пояс. Если заданы оба фильтра, запись должна пройти оба
- `--compare-*` сравнивает периоды за тот же проход, `delta` = основной − сравниваемый
- Ctrl+C или SIGTERM прерывают чтение, программа завершается с кодом 1 и сообщением `error: interrupted`

### Источники и битые строки

- поддерживаются файлы `.log` и `.txt`; файлы по glob-маске сортируются по имени
- URL должен вернуть 2xx. Таймауты стоят на соединение и заголовки, а не на все тело, поэтому большой
  удаленный лог успевает дочитаться
- формат строки — NGINX `combined`:
  `$remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent"`
- битая строка пропускается с предупреждением `WARN: skipped malformed log line (access.log:17)`, чтение продолжается
- строка длиннее 1 MiB тоже считается битой: ее остаток дочитывается и выбрасывается без загрузки в память
- `\r\n` и строка без `\n` в конце файла обрабатываются; пустые строки пропускаются молча

## MCP-сервер

```bash
./bin/incident-radar-mcp --root /var/log/nginx                       # stdio: клиент запускает сервер сам
INCIDENT_RADAR_MCP_TOKEN=secret ./bin/incident-radar-mcp \
  --transport http --addr 127.0.0.1:8765 --root /var/log/nginx       # streamable HTTP на /mcp
```

### Tools

**`list_sources`** `{glob?}` — `.log`/`.txt` файлы внутри разрешенных каталогов с размером и временем изменения.
Пути относительно `--root`. Это первый шаг агента.

**`analyze_logs`** `{path, from?, to?, since?, until?, bucket?, compareFrom?, compareTo?, top?}` — отчет
в `structuredContent` по [JSON Schema](schema/report.schema.json) и та же JSON-строка в `content` для клиентов
без поддержки structured output. Входная схема ограничивает `bucket` значениями `1m|5m|15m|1h`, а `top` диапазоном `1..100`,
чтобы небольшим локальным моделям было проще вызвать tool правильно.

Ошибки (неверный аргумент, путь вне root, лимит, таймаут) возвращаются как tool error с понятным текстом
на английском, например `timeline would have 1440 points (max 300): use a larger bucket or a narrower since/until window`.
Агент может прочитать такую ошибку и повторить вызов с исправленными аргументами.

Сервер передает клиенту `instructions` с рекомендуемым порядком работы: `list_sources` → `analyze_logs`
без bucket (итоги и `timeRange`) → `analyze_logs` с узким `since/until` и `bucket=1m`.

### Безопасность

| Флаг                     | По умолчанию     | Что ограничивает                                                     |
| ------------------------ | ---------------- | -------------------------------------------------------------------- |
| `--root`                 | `.`              | каталог логов, относительные пути считаются от него                  |
| `--allow-dir`            | —                | дополнительные каталоги, флаг можно повторять                        |
| `--allow-url`            | —                | разрешенные префиксы URL (схема + хост + путь), флаг можно повторять; без него URL запрещены |
| `--max-bytes`            | 512 MiB          | сколько байт можно прочитать за один вызов (сумма по всем файлам)    |
| `--max-files`            | 100              | сколько файлов может найти glob                                      |
| `--call-timeout`         | 60s              | время одного вызова                                                  |
| `--max-concurrent`       | 4                | параллельные вызовы, остальные ждут своей очереди                    |
| `--max-timeline-points`  | 300              | длина временного ряда, чтобы ответ поместился в контекст модели      |
| `INCIDENT_RADAR_MCP_TOKEN` | —              | Bearer-токен для `/mcp`; без него сервер на не-loopback адресе пишет предупреждение |

- путь проверяется до обращения к диску (`../`, абсолютные пути), а затем еще раз после раскрытия симлинков.
  Поэтому по ошибкам нельзя узнать, существуют ли файлы вне root
- каждый редирект URL заново проверяется по allowlist; URL с `user:pass@` отклоняется
- stdin для tools выключен (`--allow-stdin` нужен только при встраивании)
- HTTP: `http.CrossOriginProtection`, защита SDK от DNS rebinding, лимит тела запроса, `ReadHeaderTimeout`,
  `/healthz` без токена для проб
- логи пишутся через `slog` в stderr (JSON или text): старт с конфигурацией, каждый вызов tool с
  `durationMs`, ошибкой и признаком `usageError`, HTTP-запросы со статусом. stdout отдан протоколу stdio
- SIGINT/SIGTERM: HTTP-сервер ждет активные запросы до 10 с, затем закрывает соединения

### Подключение из LangGraph

```python
from langchain_mcp_adapters.client import MultiServerMCPClient

client = MultiServerMCPClient({
    "incident-radar": {
        "transport": "streamable_http",
        "url": "http://127.0.0.1:8765/mcp",
        "headers": {"Authorization": "Bearer secret"},
    },
    # или локально по stdio:
    # "incident-radar": {"transport": "stdio", "command": "./bin/incident-radar-mcp", "args": ["--root", "/var/log/nginx"]},
})
tools = await client.get_tools()  # analyze_logs, list_sources -> передать в create_react_agent / ToolNode
```

Проверено с `langchain-mcp-adapters` 0.3.2, `mcp` (Python) 1.30 и 2.3 по обоим транспортам.

### Docker

```bash
make docker VERSION=0.1.0
docker run --rm -p 8765:8765 -e INCIDENT_RADAR_MCP_TOKEN=secret \
  -v /var/log/nginx:/logs:ro --read-only --cap-drop ALL incident-radar:0.1.0
```

Образ весит ~30 MB: distroless static, пользователь `nonroot`, оба бинарника. По умолчанию запускается
MCP по HTTP на `0.0.0.0:8765` с `--root /logs`. CLI вызывается через `docker exec <container> incident-radar ...`.

## JSON-контракт

Отчет — это версионированный контракт: `schemaVersion` (сейчас `1.0.0`) и файл
[schema/report.schema.json](schema/report.schema.json) (JSON Schema 2020-12). Схема генерируется из Go-типов
(`make schema`), тест падает, если файл разошелся с кодом. Тот же объект схемы MCP-сервер отдает
как `outputSchema`, и SDK проверяет по нему каждый ответ.

| Поле                  | Что это                                                                    |
| --------------------- | -------------------------------------------------------------------------- |
| `schemaVersion`       | версия контракта                                                           |
| `files`, `filesCount` | прочитанные источники (`stdin` для `-`)                                    |
| `processedLinesCount` | непустые строки, нормальные и битые                                        |
| `malformedLinesCount` | пропущенные битые строки                                                   |
| `timeRange`           | первая и последняя корректная запись **до** фильтров — какой период вообще покрыт логами |
| `window`              | примененные `since`/`until`, есть только если они заданы                   |
| `totalRequestsCount`  | запросы основного периода                                                  |
| `responseCodes`       | код → количество                                                           |
| `statusClasses`       | `1xx`…`5xx` → количество                                                   |
| `errorRate`           | доля 5xx, 0..1, 4 знака                                                    |
| `clientErrorRate`     | доля 4xx                                                                   |
| `resources`           | top-N ресурсов: `resource`, `totalRequestsCount`                           |
| `topErrorResources`   | top-N ресурсов по числу 5xx: `resource`, `count`                           |
| `topIPs`              | top-N IP: `ip`, `count`                                                    |
| `methods`             | все методы: `method`, `count`                                              |
| `responseSizeInBytes` | `average` (2 знака), `max`, `p95`                                          |
| `protocols`           | уникальные протоколы по алфавиту                                           |
| `requestsByWeekday`   | 7 дней с понедельника: `weekday`, `weekdayCount`                           |
| `comparison`          | второй период и `delta`, есть только с `--compare-*`                       |
| `timeline`            | есть только с `bucket`: `bucket` и `points[]` = `start`, `requests`, `1xx`…`5xx`, `errorRate`, `p95SizeBytes` |

- все время в отчете указано в RFC3339 UTC; интервалы ряда выровнены по UTC (`5m` начинается в :00, :05, …)
- ряд идет от первого до последнего непустого интервала **без пропусков**: минута без запросов
  дает точку с нулями. Ноль трафика — тоже сигнал инцидента
- списки никогда не равны `null`, в пустом отчете это `[]`
- в формате combined нет времени ответа, поэтому `p95` считается по размеру ответа, а не по латентности

## Как считается

- **один проход**: каждая запись один раз раскладывается по всем сборщикам: основной период, окно, период
  сравнения, агрегаты инцидента, временной ряд. Вторая метрика не означает второе чтение лога
- **p95** считается методом nearest-rank: значения сортируются, берется элемент с однобазовым номером `ceil(0.95 × N)`.
  Для `100, 200, 300, 400, 500` p95 = `500`. Пустой набор дает `0`
- **сортировка top-списков**: по убыванию количества, при равенстве по имени. Результат воспроизводим и не зависит
  от порядка `map` и файловой системы
- **память**: строки не накапливаются, буфер строки ограничен 1 MiB. Для p95 хранятся размеры ответов (O(N) int),
  для top — словари уникальных ресурсов и IP

## Exit codes (CLI)

| Код | Значение |
| --: | -------- |
| `0` | успех |
| `1` | внутренняя ошибка (ошибка записи, паника) или прерывание по сигналу |
| `2` | ошибка использования или источника: флаг, формат, дата, `from >= to`, неверный `bucket`/`top`, нет файла, пустой glob, HTTP не 2xx, неверное расширение, существующий отчет, превышен лимит |

## Устройство

```text
cmd/
  incident-radar/        CLI: сигналы -> app.Run
  incident-radar-mcp/    MCP-сервер: флаги, slog, выбор транспорта
  log-server/            локальный HTTP-сервер с sample_logs для ручной проверки URL
internal/
  cli/        разбор и проверка флагов
  source/     поиск источников (файл, glob, URL) и открытие потока
  parser/     строка combined -> LogRecord
  stats/      базовая статистика и фильтр по датам
  report/     базовый отчет и рендер JSON/Markdown/AsciiDoc
  output/     запись отчета без перезаписи
  app/        тонкая обертка CLI: флаги -> analyze.Run -> рендер -> файл/stdout -> exit code
  analyze/    движок: Options, Run, окно since/until, временной ряд, агрегаты инцидента,
              Policy (allowlist, лимиты), ListSources, рендер дополнительных разделов
  mcpserver/  tools, JSON Schema отчета, stdio и streamable HTTP, токен, access-лог, graceful shutdown
schema/       report.schema.json — контракт отчета
sample_logs/  access.txt, mixed.txt и incident.log с инцидентом в 08:17 MSK
```

CLI и MCP вызывают один и тот же `analyze.Run(ctx, Options)`, поэтому CLI и tool дают одинаковые цифры.
Ограничения для агента задаются через `analyze.Policy`; в CLI она равна `nil`, то есть ограничений нет.

## Разработка

```bash
make test        # тесты + coverage.html
make test-race   # race detector
make check       # go mod tidy -diff, golangci-lint (gofumpt, goimports, gosec, revive, errorlint, ...)
make schema      # перегенерировать schema/report.schema.json
make serve       # sample_logs по http://127.0.0.1:8000/
make mcp         # MCP по stdio на sample_logs
make mcp-http    # MCP по HTTP на 127.0.0.1:8765
```

Что проверяют тесты (покрытие ~93%):

- **потоки**: управляемые `io.Reader`, побайтовое чтение, строки длиннее 1 MiB без больших файлов, обрыв потока, отмена context
- **политика**: `../`, симлинк наружу, несуществующий файл вне root, префиксы URL и редиректы, лимиты байт и файлов
- **MCP**: tools через in-memory транспорт SDK, тот же сценарий по streamable HTTP с токеном, e2e через запуск
  бинарника подпроцессом (`mcp.CommandTransport`)
- **CLI**: e2e через собранный бинарник с проверкой настоящих exit codes, stdin через pipe
- **контракт**: файл схемы совпадает с кодом, реальный отчет проходит валидацию

CI в GitHub Actions ([.github/workflows/ci.yml](.github/workflows/ci.yml)) запускает линтеры, тесты с `-race`
и сборку бинарников и Docker-образа.

## Ограничения

- только формат NGINX `combined`, других форматов нет
- glob работает по правилам `filepath.Glob`, без рекурсивного `**`
- p95 точный, но требует памяти по числу записей; для очень больших логов его стоит заменить на гистограмму
  или t-digest
- в логе нет времени ответа, поэтому латентность не считается
