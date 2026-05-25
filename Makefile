GO := /home/adevsh/.gvm/gos/go1.24.13/bin/go
PCOMPOSE := podman compose

.PHONY: test stress build lint upstream-up upstream-down upstream-logs upstream-ps demo-rivus demo-api demo-static

test:
	$(GO) test ./...

stress:
	$(GO) test -v -race -count=1 -timeout=120s ./stress/...

build:
	$(GO) build -o bin/rivus .

lint:
	$(GO) vet ./...

upstream-up:
	$(PCOMPOSE) -f docker-compose.yml up -d --build

upstream-down:
	$(PCOMPOSE) -f docker-compose.yml down

upstream-logs:
	$(PCOMPOSE) -f docker-compose.yml logs -f

upstream-ps:
	$(PCOMPOSE) -f docker-compose.yml ps

demo-rivus:
	./bin/rivus --config config.example.json

demo-api:
	curl -sS "http://localhost:8080/api/ping?from=make"

demo-static:
	curl -sS "http://localhost:8080/static/app.js?delay_ms=20"
