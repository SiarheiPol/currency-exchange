.PHONY: check test test-integration test-fakeprovider coverage coverage-html lint generate \
        build build-server build-fakeprovider \
        run run-fakeprovider \
        migrate-up migrate-down \
        docker-build-server docker-build-fakeprovider \
        compose-validate \
        loadtest loadtest-coalesce loadtest-read loadtest-burst loadtest-fail

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
	docker build --target server -t currency-exchange-server:dev .

docker-build-fakeprovider:
	docker build --target fakeprovider -t currency-exchange-fakeprovider:dev .

compose-validate:
	docker compose config --quiet
	docker run --rm --entrypoint="" -v "$(PWD)/deploy/prometheus:/etc/prometheus:ro" \
		prom/prometheus:v2.55.1 promtool check config /etc/prometheus/prometheus.yml

loadtest:
	docker compose --profile loadtest run --rm \
		$(if $(LOADTEST_DURATION),-e LOADTEST_DURATION=$(LOADTEST_DURATION)) \
		$(if $(LOADTEST_RATE),-e LOADTEST_RATE=$(LOADTEST_RATE)) \
		k6 run /scripts/profile1.js

loadtest-coalesce:
	docker compose --profile loadtest run --rm \
		k6 run /scripts/profile4.js

loadtest-read:
	docker compose --profile loadtest run --rm \
		$(if $(LOADTEST_DURATION),-e LOADTEST_DURATION=$(LOADTEST_DURATION)) \
		$(if $(LOADTEST_RATE),-e LOADTEST_RATE=$(LOADTEST_RATE)) \
		k6 run /scripts/profile2.js

loadtest-burst:
	docker compose --profile loadtest run --rm \
		$(if $(LOADTEST_DURATION),-e LOADTEST_DURATION=$(LOADTEST_DURATION)) \
		$(if $(LOADTEST_RATE),-e LOADTEST_RATE=$(LOADTEST_RATE)) \
		k6 run /scripts/profile3.js

loadtest-fail:
	@echo "Reminder: start the stack with FAKE_LATENCY_MIN_MS and FAKE_LATENCY_MAX_MS to activate latency injection."
	@echo "Example: FAKE_LATENCY_MIN_MS=500 FAKE_LATENCY_MAX_MS=2000 docker compose up -d"
	docker compose --profile loadtest run --rm \
		$(if $(LOADTEST_DURATION),-e LOADTEST_DURATION=$(LOADTEST_DURATION)) \
		$(if $(LOADTEST_RATE),-e LOADTEST_RATE=$(LOADTEST_RATE)) \
		k6 run /scripts/profile5.js
