.PHONY: check test lint generate run migrate-up migrate-down

check: generate
	git diff --exit-code
	go test -race ./...
	golangci-lint run

test:
	go test -race ./...

lint:
	golangci-lint run

generate:
	go generate ./...

run:
	go run ./cmd/server

migrate-up:
	go tool migrate -path migrations -database "$(DB_DSN)" up

migrate-down:
	go tool migrate -path migrations -database "$(DB_DSN)" down 1
