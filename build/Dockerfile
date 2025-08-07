### Builder stage
FROM golang:1.23.0-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY internal ./internal
COPY cmd ./cmd
COPY scripts ./scripts
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o gotube ./cmd/server

### Final stage
FROM alpine:3.18
WORKDIR /app
COPY --from=builder /app/gotube .
COPY scripts/migrations.sql /migrations/migrations.sql
EXPOSE 8080
ENTRYPOINT ["./gotube"]