.PHONY: help build windows linux all test run clean tidy

BINARY=fc-proxy
BUILD_DIR=./build
LDFLAGS=-ldflags="-s -w"

.DEFAULT_GOAL := help

help: ## Show the available commands
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build for this machine
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/fc-proxy
	@cp fc-proxy.env.example $(BUILD_DIR)/
	@echo "built $(BUILD_DIR)/$(BINARY)"

windows: ## Build the Windows exe for the jump server
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY).exe ./cmd/fc-proxy
	@cp fc-proxy.env.example $(BUILD_DIR)/
	@echo "built $(BUILD_DIR)/$(BINARY).exe"
	@echo "copy it to the jump server along with fc-proxy.env.example, renamed fc-proxy.env"

linux: ## Build a linux/amd64 binary
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux ./cmd/fc-proxy
	@echo "built $(BUILD_DIR)/$(BINARY)-linux"

all: build windows linux ## Build for every target

test: ## Run the test suite
	@go test ./...

run: ## Build and start the proxy locally
	@go run ./cmd/fc-proxy serve

tidy: ## Tidy the module
	@go mod tidy

clean: ## Remove build artifacts
	@rm -rf $(BUILD_DIR)
	@echo "cleaned"
