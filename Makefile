.PHONY: check test test-integration test-fakeprovider coverage coverage-html lint generate \
        build build-server build-fakeprovider \
        run run-fakeprovider \
        migrate-up migrate-down \
        docker-build-server docker-build-fakeprovider

COVERAGE_FILE ?= coverage.out

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

coverage:
	go test -race -covermode=atomic -coverpkg=./... -coverprofile=$(COVERAGE_FILE) ./...
	go tool cover -func=$(COVERAGE_FILE) | tail -n 1

coverage-html: coverage
	go tool cover -html=$(COVERAGE_FILE) -o coverage.html

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

docker-build-server:
	docker build --target server -t plata-server:dev .

docker-build-fakeprovider:
	docker build --target fakeprovider -t plata-fakeprovider:dev .
