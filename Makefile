BINARY := asokit
PKG := ./cmd/asokit
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test lint fmt vet tidy clean install

build: ## Build the binary into ./$(BINARY)
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

test: ## Run tests
	go test ./...

vet: ## Run go vet
	go vet ./...

lint: vet ## Run go vet and golangci-lint if present
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed; ran go vet only"; \
	fi

fmt: ## Format code
	gofmt -s -w .

tidy: ## Tidy go.mod/go.sum
	go mod tidy

install: ## Install the binary into GOBIN
	go install -ldflags "$(LDFLAGS)" $(PKG)

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist/
