APP_NAME := codex-switch
BUILD_DIR := .build

ifeq ($(OS),Windows_NT)
SHELL := powershell.exe
.SHELLFLAGS := -NoProfile -Command
EXE_SUFFIX := .exe
DEFAULT_HOME := $(USERPROFILE)
MKDIR_CMD = New-Item -ItemType Directory -Force "$(1)" | Out-Null
COPY_CMD = Copy-Item "$(1)" "$(2)" -Force
REMOVE_DIR_CMD = if (Test-Path "$(1)") { Remove-Item -Recurse -Force "$(1)" }
else
EXE_SUFFIX :=
DEFAULT_HOME := $(HOME)
MKDIR_CMD = mkdir -p "$(1)"
COPY_CMD = cp "$(1)" "$(2)"
REMOVE_DIR_CMD = rm -rf "$(1)"
endif

BIN_DIR ?= $(DEFAULT_HOME)/bin
BINARY := $(BUILD_DIR)/$(APP_NAME)$(EXE_SUFFIX)
INSTALL_PATH := $(BIN_DIR)/$(APP_NAME)$(EXE_SUFFIX)
export GOCACHE := $(abspath $(BUILD_DIR))/gocache
export GOMODCACHE := $(abspath $(BUILD_DIR))/gomodcache

.PHONY: help build test install enable clean

help:
	@echo "Available targets:"
	@echo "  make build     Compile $(APP_NAME)"
	@echo "  make test      Run tests"
	@echo "  make install   Compile and install to $(BIN_DIR)"
	@echo "  make enable    Add $(BIN_DIR) to the user PATH on Windows"
	@echo "  make clean     Remove build output"

build:
	@$(call MKDIR_CMD,$(BUILD_DIR))
	@$(call MKDIR_CMD,$(GOCACHE))
	@$(call MKDIR_CMD,$(GOMODCACHE))
	go build -o "$(BINARY)" .
	@echo "Built: $(BINARY)"

test:
	@$(call MKDIR_CMD,$(BUILD_DIR))
	@$(call MKDIR_CMD,$(GOCACHE))
	@$(call MKDIR_CMD,$(GOMODCACHE))
	go test ./...

install: build
	@$(call MKDIR_CMD,$(BIN_DIR))
	@$(call COPY_CMD,$(BINARY),$(INSTALL_PATH))
	@echo "Installed: $(INSTALL_PATH)"
	@echo "If '$(BIN_DIR)' is not in PATH yet, add it and reopen your terminal."

enable:
ifeq ($(OS),Windows_NT)
	@$$bin = [System.IO.Path]::GetFullPath("$(BIN_DIR)"); $$userPath = [Environment]::GetEnvironmentVariable("Path", "User"); $$parts = @(); if ($$userPath) { $$parts = $$userPath -split ';' | Where-Object { $$_.Trim() -ne '' } }; if ($$parts -contains $$bin) { Write-Output "$$bin is already in the user PATH." } else { $$newPath = if ($$parts.Count -gt 0) { ($$parts + $$bin) -join ';' } else { $$bin }; [Environment]::SetEnvironmentVariable("Path", $$newPath, "User"); Write-Output "Added $$bin to the user PATH. Reopen your terminal to use it." }
else
	@echo "make enable is currently only implemented for Windows."
endif

clean:
	@$(call REMOVE_DIR_CMD,$(BUILD_DIR))
