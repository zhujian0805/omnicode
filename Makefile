.PHONY: help build rebuild run test test-race fmt vet install uninstall clean prepare-build-dir

APP_NAME := omnicode
CMD_PATH := ./cmd/omnicode
BUILD_DIR := build
INSTALL_DIR ?= $(HOME)/.local/bin
GO ?= go

ifeq ($(OS),Windows_NT)
	BINARY := $(BUILD_DIR)/$(APP_NAME).exe
	INSTALL_PATH := $(INSTALL_DIR)/$(APP_NAME).exe
else
	BINARY := $(BUILD_DIR)/$(APP_NAME)
	INSTALL_PATH := $(INSTALL_DIR)/$(APP_NAME)
endif

help:
	@printf "Available targets:\n"
	@printf "  %-12s %s\n" "build" "Build the CLI into $(BINARY)"
	@printf "  %-12s %s\n" "rebuild" "Clean and rebuild the CLI"
	@printf "  %-12s %s\n" "run" "Run the CLI with go run"
	@printf "  %-12s %s\n" "test" "Run all Go tests"
	@printf "  %-12s %s\n" "test-race" "Run all Go tests with the race detector"
	@printf "  %-12s %s\n" "fmt" "Format Go source files"
	@printf "  %-12s %s\n" "vet" "Run go vet across all packages"
	@printf "  %-12s %s\n" "install" "Install the built binary to $(INSTALL_PATH)"
	@printf "  %-12s %s\n" "uninstall" "Remove the installed binary"
	@printf "  %-12s %s\n" "clean" "Remove build artifacts and Go caches"

prepare-build-dir:
	mkdir -p $(BUILD_DIR)

build: prepare-build-dir
	$(GO) build -o $(BINARY) $(CMD_PATH)

rebuild: clean build

run:
	$(GO) run $(CMD_PATH)

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_PATH)
	chmod +x $(INSTALL_PATH)

uninstall:
	rm -f $(INSTALL_PATH)

clean:
	rm -rf $(BUILD_DIR)
	$(GO) clean -cache -testcache
