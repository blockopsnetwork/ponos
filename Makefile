.PHONY: build run server test-all clean

PONOS_DIR := ponos-new

build-ponos:
	@echo "Building Ponos..."
	go build -o bin/ponos ./cmd

run: build-ponos
	@echo "Running Ponos Server..."
	go run cmd/*.go server

clean:
	rm -rf $(PONOS_DIR)/bin
