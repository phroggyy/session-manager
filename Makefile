.PHONY: build test lint clean install

BINARY_NAME=sm
BUILD_DIR=bin

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/sm

test:
	go test ./...

test-ginkgo:
	ginkgo ./...

lint:
	golangci-lint run

clean:
	rm -rf $(BUILD_DIR)

install: build
	cp $(BUILD_DIR)/$(BINARY_NAME) $(GOPATH)/bin/$(BINARY_NAME)

run: build
	./$(BUILD_DIR)/$(BINARY_NAME)

.DEFAULT_GOAL := build
