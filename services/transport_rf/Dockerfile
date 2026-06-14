FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /transport_service .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /
COPY --from=builder /transport_service /transport_service
EXPOSE 50054
HEALTHCHECK --interval=10s --timeout=2s --start-period=5s \
    CMD wget -qO- http://localhost:50054/health || exit 1
ENTRYPOINT ["/transport_service"]
