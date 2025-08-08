    .PHONY: run test migrate lint

    run:
	go run ./cmd/server

    test:
	go test ./...

    migrate:
	atlas schema apply -u "$${POSTGRES_URL}" -f migrations/schema.hcl --dev-url "docker://postgres/16/dev"

    lint:
	golangci-lint run || true
