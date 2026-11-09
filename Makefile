# Build del monitor para linux/arm64 con el commit embebido.
#
#   make                # equivale a make dist-monitor
#   make dist-monitor   # release ARM64; exige árbol limpio
#   make test
#   make vet
#
# El servidor de despliegue no necesita Git ni Go: el binario se consulta con
# `oci-infrastructure-monitor version`.

.DEFAULT_GOAL := dist-monitor

GO          ?= go
VERSION     := $(shell git describe --tags --always)
GIT_COMMIT  := $(shell git rev-parse --short=12 HEAD)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GO_LDFLAGS  := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(GIT_COMMIT) -X main.buildDate=$(BUILD_DATE)"

DIST        := dist
BIN_ARM64   := $(DIST)/oci-infrastructure-monitor-linux-arm64

.PHONY: dist-monitor check-clean test vet

check-clean:
	@git rev-parse --is-inside-work-tree >/dev/null 2>&1 || { \
		echo "error: no es un repositorio git; se necesita Git para el commit" >&2; \
		exit 1; \
	}
	@test -z "$$(git status --porcelain)" || { \
		echo "error: árbol sucio; commitea los cambios antes de compilar un release" >&2; \
		git status --short >&2; \
		exit 1; \
	}

dist-monitor: check-clean
	@mkdir -p $(DIST)
	@echo "Compilando $(BIN_ARM64) (commit: $(GIT_COMMIT))..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -buildvcs=true $(GO_LDFLAGS) -o $(BIN_ARM64) ./cmd/monitor
	@file $(BIN_ARM64)
	@$(GO) version -m $(BIN_ARM64) | grep 'vcs\.'
	@sha256sum $(BIN_ARM64) > $(DIST)/SHA256SUMS
	@(cd $(DIST) && sha256sum $(notdir $(BIN_ARM64))) > $(DIST)/SHA256SUMS.deploy

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
