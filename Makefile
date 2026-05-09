.PHONY: check test test-integration lint generate build run migrate-up migrate-down

check: generate
	git diff --exit-code
	go test -race ./...
	golangci-lint run

test:
	go test -race ./...

test-integration:
	go test -race -tags integration ./...

lint:
	golangci-lint run

generate:
	go generate ./...

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

migrate-up:
	go tool migrate -path migrations -database "$(DB_DSN)" up

migrate-down:
	go tool migrate -path migrations -database "$(DB_DSN)" down 1
