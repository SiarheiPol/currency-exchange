.PHONY: check test test-integration test-fakeprovider lint generate \
        build build-server build-fakeprovider \
        run run-fakeprovider \
        migrate-up migrate-down

check: generate
	git diff --exit-code
	go test -race ./...
	golangci-lint run

test:
	go test -race ./...

test-integration:
	go test -race -tags integration ./...

test-fakeprovider:
	go test -race ./cmd/fakeprovider/...

lint:
	golangci-lint run

generate:
	go generate ./...

build: build-server build-fakeprovider

build-server:
	go build -o bin/server ./cmd/server

build-fakeprovider:
	go build -o bin/fakeprovider ./cmd/fakeprovider

run:
	go run ./cmd/server

run-fakeprovider:
	go run ./cmd/fakeprovider

migrate-up:
	go tool migrate -path migrations -database "$(DB_DSN)" up

migrate-down:
	go tool migrate -path migrations -database "$(DB_DSN)" down 1
