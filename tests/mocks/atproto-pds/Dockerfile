FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY main.go .
COPY main_test.go .
# Initialize a minimal module (the mock is a standalone binary)
RUN go mod init athena-mock-atproto && go build -o mock-atproto .

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/mock-atproto .
EXPOSE 8080
CMD ["./mock-atproto"]
