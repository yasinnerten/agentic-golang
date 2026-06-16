GO ?= go
DATABASE_URL ?= postgres://agentic:agentic@localhost:5432/agentic?sslmode=disable

.PHONY: fmt tidy vet test build check migrate compose-up compose-down example

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

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
