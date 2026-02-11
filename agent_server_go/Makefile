APP_NAME=agent-server

.PHONY: help
help:
	@echo "make run        - run API server"
	@echo "make build      - build binary"
	@echo "make test       - run tests"
	@echo "make lint       - run gofmt and go vet"
	@echo "make compose-up - start local dependencies"
	@echo "make compose-down - stop local dependencies"

.PHONY: run
run:
	go run ./cmd/api

.PHONY: build
build:
	go build -o ./bin/$(APP_NAME) ./cmd/api

.PHONY: test
test:
	go test ./...

.PHONY: lint
lint:
	gofmt -w .
	go vet ./...

.PHONY: compose-up
compose-up:
	docker compose -f deploy/docker-compose.yml up -d

.PHONY: compose-down
compose-down:
	docker compose -f deploy/docker-compose.yml down -v
