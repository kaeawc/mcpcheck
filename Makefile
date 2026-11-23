.PHONY: build test vet lint fmt complexity security licenses tidy ci e2e clean release-check validate-workflows all

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -s -w -X main.version=$(VERSION)
BIN ?= mcpcheck

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/mcpcheck/

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -s -w .

lint:
	golangci-lint run

complexity:
	gocyclo -over 10 -ignore '_test\.go$$' .

security:
	gosec ./...

licenses:
	go-licenses report ./...

tidy:
	go mod tidy

ci: vet test complexity lint security licenses

# Builds the reference servers in tests/servers (needs uv, node, cargo,
# go) and runs the static and live intakes against each of them.
e2e:
	tests/servers/setup.sh
	MCPCHECK_E2E=1 go test ./tests/e2e/ -count=1 -v

release-check:
	bash scripts/release-check.sh

validate-workflows:
	bash scripts/validate-workflows.sh

clean:
	rm -f $(BIN) junit-report.xml gosec-report.xml

all: build vet test
