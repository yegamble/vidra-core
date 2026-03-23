FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY main.go .
COPY main_test.go .
RUN go mod init athena-mock-activitypub && go build -o mock-activitypub .

FROM alpine:3.18
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/mock-activitypub .
EXPOSE 8080
CMD ["./mock-activitypub"]
