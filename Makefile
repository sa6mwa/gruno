BIN ?= bin/gru
INSTALL_PATH ?= /usr/local/bin
VER ?= $(shell go run pkt.systems/version/println -semver)

.PHONY: all build install test test-short test-windows clean release

all: build

test:
	go test -count=1 -cover -v ./...

build: test-short
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/gru

install:
	install -m 0755 $(BIN) $(INSTALL_PATH)/gru

test-short:
	go test -short -count=1 ./...

test-windows:
	WINEDEBUG=-all GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -short -count=1 -exec "wine" ./...

clean:
	rm -rf $(dir $(BIN))

release:
	@release_dir="$(dir $(BIN))release"; \
	rm -rf "$$release_dir"; \
	mkdir -p "$$release_dir"; \
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		out_dir="$$release_dir/$${os}-$${arch}"; \
		mkdir -p "$$out_dir"; \
		bin_name="gru"; \
		if [ "$$os" = "windows" ]; then bin_name="gru.exe"; fi; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" go build -trimpath -ldflags="-s -w" -o "$$out_dir/$$bin_name" ./cmd/gru; \
		z="$$release_dir/gru-$(VER)-$${os}-$${arch}.zip"; \
		echo "$$z"; \
		zip -j "$$z" "$$out_dir/$$bin_name"; \
	done
