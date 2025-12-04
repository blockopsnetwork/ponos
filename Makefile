.PHONY: build run server test-all clean

PONOS_DIR := ponos-new

build-ponos:
	@echo "Building Ponos..."
	cd $(PONOS_DIR) && go build -o bin/ponos ./cmd

# Run Ponos Server
run-ponos: build-ponos
	@echo "Running Ponos Server..."
	cd $(PONOS_DIR) && ./bin/ponos server

# Run Ponos TUI
run-ponos-tui: build-ponos
	@echo "Running Ponos TUI..."
	cd $(PONOS_DIR) && ./bin/ponos

# Run Agent Core (Python)
run-core:
	@echo "Running Agent Core..."
	cd $(CORE_DIR) && uvicorn agent_core.main:app --reload --port 8001

# Run all tests
test-all: test-ponos test-core

test-ponos:
	@echo "Testing Ponos..."
	cd $(PONOS_DIR) && go test ./...

test-core:
	@echo "Testing Agent Core..."
	cd $(CORE_DIR) && pytest

# Clean build artifacts
clean:
	rm -rf $(PONOS_DIR)/bin
	find . -type d -name "__pycache__" -exec rm -rf {} +
