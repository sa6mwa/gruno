BIN ?= bin/gru
INSTALL_PATH ?= /usr/local/bin

.PHONY: all build install test test-short

all: build

test:
	go test -count=1 -cover -v ./...

build: test-short
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/gru

install: build
	install -m 0755 $(BIN) $(INSTALL_PATH)/gru

.PHONY: test-short
test-short:
	go test -short -count=1 ./...
