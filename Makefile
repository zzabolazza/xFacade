# xFacade — Wails desktop app + Launch API for xLauncher.
# Usage: make -C xFacade install | make test | make uninstall
# Prefers an existing build/bin artifact; runs `wails build` when available and needed.

.PHONY: help test install uninstall normalize-artifact-names build-host _require-build-artifact

.DEFAULT_GOAL := help

ifeq ($(OS),Windows_NT)
    ifneq ($(strip $(HOME)),)
        USER_HOME_RAW := $(HOME)
    else
        USER_HOME_RAW := $(USERPROFILE)
    endif
    USER_HOME := $(shell cygpath -u "$(USER_HOME_RAW)" 2>/dev/null)
    ifeq ($(strip $(USER_HOME)),)
        $(error Cannot resolve Windows home directory. Run Make using Git Bash with cygpath available)
    endif
else
    USER_HOME := $(HOME)
endif

PREFIX ?= $(USER_HOME)/.xTools
BIN := xFacade
LIB_NAME := xfacade
INSTALL_LIB := $(PREFIX)/lib/$(LIB_NAME)
INSTALL_BIN := $(PREFIX)/bin/$(BIN)
INSTALL_DESC := $(PREFIX)/bin/$(BIN).desc
BUILD_BIN_DIR := build/bin
UNAME_S := $(shell uname -s 2>/dev/null)

help:
	@echo "Targets: test, install, uninstall, build-host"
	@echo "PREFIX defaults to $(USER_HOME)/.xTools"
	@echo "BIN=$(BIN)  lib=$(INSTALL_LIB)"

test:
	go test ./backend/internal/launchcode/ ./backend/internal/apppath/ ./backend/internal/config/

# Prefer PATH, then $(go env GOPATH)/bin (common local install location).
WAILS ?= $(shell command -v wails 2>/dev/null || { gopath=$$(go env GOPATH 2>/dev/null); test -n "$$gopath" && test -x "$$gopath/bin/wails" && echo "$$gopath/bin/wails"; })

build-host:
	@if [ -n "$(WAILS)" ]; then \
		echo "Building $(BIN) with $(WAILS)..."; \
		BROWSERSLIST_IGNORE_OLD_DATA=1 "$(WAILS)" build -o $(BIN); \
	else \
		echo "wails not found; will reuse existing $(BUILD_BIN_DIR) artifact if present"; \
	fi
	@$(MAKE) --no-print-directory _require-build-artifact

_require-build-artifact:
	@set -e; \
	if [ "$(UNAME_S)" = "Darwin" ]; then \
		if [ -x "$(BUILD_BIN_DIR)/$(BIN).app/Contents/MacOS/$(BIN)" ] || [ -x "$(BUILD_BIN_DIR)/$(BIN)" ]; then \
			exit 0; \
		fi; \
		echo "error: missing macOS build under $(BUILD_BIN_DIR)/$(BIN).app (run wails build)" >&2; exit 1; \
	elif [ "$(UNAME_S)" = "Linux" ]; then \
		test -x "$(BUILD_BIN_DIR)/$(BIN)" \
			|| { echo "error: missing Linux build $(BUILD_BIN_DIR)/$(BIN) (run wails build)" >&2; exit 1; }; \
	else \
		test -f "$(BUILD_BIN_DIR)/$(BIN).exe" \
			|| { echo "error: missing Windows build $(BUILD_BIN_DIR)/$(BIN).exe (run wails build)" >&2; exit 1; }; \
	fi

install: build-host
	@test -f "$(BIN).desc" || { echo "error: missing $(BIN).desc" >&2; exit 1; }
	install -d "$(PREFIX)/bin"
	$(MAKE) --no-print-directory normalize-artifact-names NORMALIZE_DIR="$(PREFIX)/bin"
	rm -rf "$(INSTALL_LIB)"
	install -d "$(INSTALL_LIB)"
	@set -e; \
	case "$(UNAME_S)" in \
	Darwin) \
		if [ -d "$(BUILD_BIN_DIR)/$(BIN).app" ]; then \
			ditto "$(BUILD_BIN_DIR)/$(BIN).app" "$(INSTALL_LIB)/$(BIN).app"; \
			MACOS_DIR="$(INSTALL_LIB)/$(BIN).app/Contents/MacOS"; \
			if [ ! -f "$$MACOS_DIR/config.yaml" ]; then \
				install -m 0644 publish/config.init.mac.yaml "$$MACOS_DIR/config.yaml"; \
			fi; \
			REL="$(BIN).app/Contents/MacOS/$(BIN)"; \
		else \
			install -m 0755 "$(BUILD_BIN_DIR)/$(BIN)" "$(INSTALL_LIB)/$(BIN)"; \
			install -m 0644 publish/config.init.mac.yaml "$(INSTALL_LIB)/config.yaml"; \
			REL="$(BIN)"; \
		fi; \
		;; \
	Linux) \
		install -m 0755 "$(BUILD_BIN_DIR)/$(BIN)" "$(INSTALL_LIB)/$(BIN)"; \
		if [ -f publish/config.init.linux.yaml ]; then \
			install -m 0644 publish/config.init.linux.yaml "$(INSTALL_LIB)/config.yaml"; \
		else \
			install -m 0644 publish/config.init.yaml "$(INSTALL_LIB)/config.yaml"; \
		fi; \
		REL="$(BIN)"; \
		;; \
	*) \
		install -m 0755 "$(BUILD_BIN_DIR)/$(BIN).exe" "$(INSTALL_LIB)/$(BIN).exe"; \
		install -m 0644 publish/config.init.yaml "$(INSTALL_LIB)/config.yaml"; \
		REL="$(BIN).exe"; \
		;; \
	esac; \
	{ printf '%s\n' '#!/bin/sh' \
		'ROOT="$(PREFIX)"' \
		"exec \"\$$ROOT/lib/$(LIB_NAME)/$$REL\" \"\$$@\""; } > "$(INSTALL_BIN)"
	chmod 755 "$(INSTALL_BIN)"
	install -m 0644 "$(BIN).desc" "$(INSTALL_DESC)"
	@echo "Installed $(BIN) for xLauncher"
	@echo "Binary: $(INSTALL_BIN)"
	@echo "App:    $(INSTALL_LIB)"

uninstall:
	rm -rf "$(INSTALL_LIB)"
	rm -f "$(INSTALL_BIN)" "$(INSTALL_DESC)"
	rm -f "$(PREFIX)/bin/$(BIN).exe" "$(PREFIX)/bin/xfacade" "$(PREFIX)/bin/xfacade.desc"
	@echo "Uninstalled from: $(PREFIX)/bin"

LEGACY_BIN := xfacade
CANONICAL_BIN := $(BIN)

# Normalize legacy filenames explicitly: overwriting a file on macOS does not
# necessarily update the capitalization of its existing directory entry.
normalize-artifact-names:
	@set -e; for dir in "$(NORMALIZE_DIR)"; do \
		[ -n "$$dir" ] && [ -d "$$dir" ] || continue; \
		for file in "$$dir"/*; do \
			[ -f "$$file" ] || continue; name=$${file##*/}; \
			case "$$name" in $(LEGACY_BIN)|$(LEGACY_BIN)-*|$(LEGACY_BIN).exe|$(LEGACY_BIN).desc|$(LEGACY_BIN).exe.desc) ;; *) continue ;; esac; \
			target="$$dir/$(CANONICAL_BIN)$${name#$(LEGACY_BIN)}"; \
			if [ -e "$$target" ] && [ ! "$$file" -ef "$$target" ]; then rm -f "$$file"; \
			else mv "$$file" "$$file.naming-tmp"; mv "$$file.naming-tmp" "$$target"; fi; \
		done; \
	done
