.PHONY: check test test-integration test-fakeprovider coverage coverage-html lint generate \
        build build-server build-fakeprovider \
        run run-fakeprovider \
        migrate-up migrate-down \
        docker-build-server docker-build-fakeprovider \
        compose-validate \
        demo demo-real

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

demo:
	@echo "Starting demo stack: fake provider, business-like settings, then 5000 RPS read storm."
	FAKE_LATENCY_MIN_MS=100 FAKE_LATENCY_MAX_MS=500 \
	SCHEDULER_TICK_SECONDS=30 COALESCING_WINDOW_SECONDS=5 \
	docker compose up -d
	@echo "Waiting for /readyz..."
	@for i in $$(seq 1 60); do \
		if curl -sf http://localhost:8080/readyz > /dev/null 2>&1; then \
			echo "Server ready."; break; \
		fi; \
		sleep 1; \
	done
	docker compose --profile loadtest run --rm \
		-e LOADTEST_RATE=5000 -e LOADTEST_VUS=1000 -e LOADTEST_DURATION=2m \
		k6 run /scripts/profile2.js
	@echo ""
	@echo "Demo stack is still running. Grafana: http://localhost:3000 (admin/admin)."
	@echo "Stop with: docker compose down (or 'down -v' to drop volumes)."

demo-real:
	@echo "================================================================"
	@echo "WARNING: starting with REAL upstream (currencylayer)."
	@echo "  SCHEDULER_TICK_SECONDS=120 → up to ~30 ticks/hour."
	@echo "  Check your provider quota before leaving this running."
	@echo "  Requires PROVIDER_API_KEY in .env."
	@echo "================================================================"
	PROVIDER_BASE_URL=https://api.currencylayer.com \
	SCHEDULER_TICK_SECONDS=120 COALESCING_WINDOW_SECONDS=30 \
	docker compose up -d
	@echo ""
	@echo "Stack started. Service: http://localhost:8080/healthz  Grafana: http://localhost:3000"
	@echo "Stop with: docker compose down"
