# Образ с CLI и MCP-сервером: статические бинарники на distroless, запуск от непривилегированного пользователя
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/ ./cmd/incident-radar ./cmd/incident-radar-mcp

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/incident-radar /out/incident-radar-mcp /usr/local/bin/
WORKDIR /logs
USER nonroot:nonroot
EXPOSE 8765
ENTRYPOINT ["/usr/local/bin/incident-radar-mcp"]
CMD ["--transport", "http", "--addr", "0.0.0.0:8765", "--root", "/logs"]
