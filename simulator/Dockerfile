# ============ 构建阶段 ============
FROM golang:1.21-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" \
    -o mqtt-simulator .

# ============ 运行阶段 ============
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

COPY --from=builder /app/mqtt-simulator /app/

RUN chown -R appuser:appgroup /app

USER appuser

ENTRYPOINT ["/app/mqtt-simulator"]
CMD ["--broker", "tcp://emqx:1883", "--beds", "50", "--interval", "1000", "--sepsis_mode", "normal"]
