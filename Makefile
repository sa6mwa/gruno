BIN ?= bin/gru
INSTALL_PATH ?= /usr/local/bin

.PHONY: all build install test test-short clean

all: build

test:
	go test -count=1 -cover -v ./...

build: test-short
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/gru

install:
	install -m 0755 $(BIN) $(INSTALL_PATH)/gru

test-short:
	go test -short -count=1 ./...

clean:
	rm -rf $(dir $(BIN))
