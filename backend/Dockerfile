# ============ 构建阶段 ============
FROM golang:1.21-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ) -X main.version=1.0.0" \
    -o field-hospital-server .

# ============ 运行阶段 ============
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata curl && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /app/field-hospital-server /app/
COPY --from=builder /app/config.yaml /app/
COPY --from=builder /app/frontend /app/frontend

RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080
EXPOSE 6060

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/api/beds || exit 1

ENTRYPOINT ["/app/field-hospital-server"]
