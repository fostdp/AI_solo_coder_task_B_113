FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /vap_cox_service .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /
COPY --from=builder /vap_cox_service /vap_cox_service
EXPOSE 50051
HEALTHCHECK --interval=10s --timeout=2s --start-period=5s \
    CMD wget -qO- http://localhost:50052/health || exit 1
ENTRYPOINT ["/vap_cox_service"]
