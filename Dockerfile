# Build the Go API binary.  We use a multi‑stage build so that the final
# image contains only the compiled binary and minimal runtime dependencies.

FROM golang:1.24 as builder
WORKDIR /app

# Copy go module files and download dependencies.  This leverages docker
# layer caching so subsequent builds are faster when the module files
# haven't changed.
COPY go.mod ./
RUN go mod download

# Copy the rest of the source code.
COPY . .

# Build the server binary.  We disable CGO and target linux to produce
# a statically linked binary suitable for scratch/alpine images.
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/server /server
EXPOSE 8080

# Run the compiled binary.  This container will exit if the server exits.
CMD ["/server"]