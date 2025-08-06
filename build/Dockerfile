### Builder stage
FROM golang:1.20-alpine AS builder
WORKDIR /app
COPY go.mod .
COPY internal ./internal
COPY cmd ./cmd
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o gotube ./cmd/server

### Final stage
FROM alpine:3.18
WORKDIR /app
COPY --from=builder /app/gotube .
COPY migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["./gotube"]