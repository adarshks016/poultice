BINARY  := poultice
PKG     := ./cmd/poultice
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.0.1-dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.DEFAULT_GOAL := check

.PHONY: build
build: ## Build ./bin/poultice
	@mkdir -p bin
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) $(PKG)
	@echo "built bin/$(BINARY) $(VERSION)"

.PHONY: install
install: ## Install poultice into GOBIN
	go install -trimpath -ldflags '$(LDFLAGS)' $(PKG)

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: race
race: ## Run tests with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Write and summarize a coverage profile
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format all Go sources
	gofmt -w .

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmtcheck
fmtcheck: ## Fail if any file is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; \
	fi

.PHONY: validate-recipes
validate-recipes: build ## Validate every shipped recipe
	./bin/$(BINARY) validate recipes/*.yaml

.PHONY: check
check: fmtcheck vet test validate-recipes ## Everything CI runs

.PHONY: demo
demo: build ## Heal this repository with deterministic strategies only
	./bin/$(BINARY) heal --severity low --no-ai

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin coverage.out

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
