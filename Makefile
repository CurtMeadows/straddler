BINARY      := bin/straddler
PKG         := ./cmd/straddler
INSTALL_DIR ?= /usr/local/bin

.PHONY: build install uninstall tidy vet test clean db-up db-down migrate-up migrate-down

build:
	go build -o $(BINARY) $(PKG)

# Install the binary system-wide. Override INSTALL_DIR to change destination.
#   make install
#   make install INSTALL_DIR=$(HOME)/.local/bin
install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/straddler
	@echo "Installed straddler to $(INSTALL_DIR)/straddler"

uninstall:
	rm -f $(INSTALL_DIR)/straddler
	@echo "Removed $(INSTALL_DIR)/straddler"

tidy:
	go mod tidy

vet:
	go vet ./...

test:
	go test ./... -race -count=1

clean:
	rm -rf bin/

# ── Local Postgres helpers ───────────────────────────────────────────────────

db-up:
	docker compose up -d
	@echo "Postgres ready at postgres://straddler:straddler@localhost:5432/straddler"

db-down:
	docker compose down

# ── Migration helpers (requires a running Postgres) ──────────────────────────

DSN ?= postgres://straddler:straddler@localhost:5432/straddler?sslmode=disable

migrate-up: build
	STRADDLER_DATABASE_DSN="$(DSN)" ./$(BINARY) migrate up

migrate-down: build
	STRADDLER_DATABASE_DSN="$(DSN)" ./$(BINARY) migrate down
