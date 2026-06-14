FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /scheduler_service .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /
COPY --from=builder /scheduler_service /scheduler_service
EXPOSE 50053
LABEL cpu_allocation="high"
LABEL compute_type="integer_programming"
HEALTHCHECK --interval=10s --timeout=2s --start-period=10s \
    CMD wget -qO- http://localhost:50053/health || exit 1
ENTRYPOINT ["/scheduler_service"]
