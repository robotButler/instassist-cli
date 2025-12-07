.PHONY: build install uninstall clean test help

BINARY_NAME=insta
INSTALL_PATH=/usr/local/bin
SCHEMA_PATH=/usr/local/share/insta-assist
VERSION=1.0.0

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

build: ## Build the binary
	@echo "Building $(BINARY_NAME) v$(VERSION)..."
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY_NAME) .
	@echo "Build complete: ./$(BINARY_NAME)"

install: build ## Build and install to system
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	sudo cp $(BINARY_NAME) $(INSTALL_PATH)/
	@echo "Creating schema directory at $(SCHEMA_PATH)..."
	sudo mkdir -p $(SCHEMA_PATH)
	sudo cp options.schema.json $(SCHEMA_PATH)/
	@echo "Installation complete!"
	@echo ""
	@echo "To use the schema, the binary will look for it in:"
	@echo "  1. Same directory as the binary"
	@echo "  2. Current working directory"
	@echo "  3. $(SCHEMA_PATH)/options.schema.json"

uninstall: ## Remove installed binary and schema
	@echo "Removing $(BINARY_NAME)..."
	sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	sudo rm -rf $(SCHEMA_PATH)
	@echo "Uninstall complete!"

clean: ## Remove build artifacts
	@echo "Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo "Clean complete!"

test: build ## Build and run a quick test
	@echo "Testing version flag..."
	./$(BINARY_NAME) -version
	@echo ""
	@echo "Testing help..."
	./$(BINARY_NAME) -h

run: build ## Build and run in interactive mode
	./$(BINARY_NAME)

go-install: ## Install with go install (places binary in GOBIN or GOPATH/bin as instassist)
	GO_BIN=$$(go env GOBIN); \
	if [ -z "$$GO_BIN" ]; then GO_BIN="$$(go env GOPATH)/bin"; fi; \
	go install ./...; \
	echo "Binary installed to $$GO_BIN/instassist (symlink to insta if desired: ln -sf $$GO_BIN/instassist $$GO_BIN/insta)"
