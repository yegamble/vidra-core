FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY main.go .
COPY main_test.go .
RUN go mod init vidra-mock-iota-rpc && go build -o mock-iota-rpc .

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/mock-iota-rpc .
EXPOSE 8080
CMD ["./mock-iota-rpc"]
