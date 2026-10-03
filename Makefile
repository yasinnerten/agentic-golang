GO ?= go
DATABASE_URL ?= postgres://agentic:agentic@localhost:5432/agentic?sslmode=disable

.PHONY: fmt tidy vet test test-integration build check migrate compose-up compose-down example

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

# Integration tests need a Postgres they can create/drop databases on, e.g.
# the docker-compose one: make compose-up && make test-integration
TEST_DATABASE_URL ?= postgres://agentic:agentic@localhost:5432/postgres?sslmode=disable
test-integration:
	AGENTIC_TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test -race ./...

build:
	$(GO) build ./...

check: fmt tidy vet test build

migrate:
	@for f in migrations/*.up.sql; do \
		echo "applying $$f"; \
		psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 -f "$$f"; \
	done

compose-up:
	docker compose up -d

compose-down:
	docker compose down

example:
	$(GO) run ./examples/decision-routing
