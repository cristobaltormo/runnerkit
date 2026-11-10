VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build test release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o runnerkit ./cmd/runnerkit

test:
	go vet ./...
	go test -race -count=1 ./...

release:
	rm -rf dist && mkdir dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; name=runnerkit-$$os-$$arch; \
		[ $$os = windows ] && name=$$name.exe; \
		echo $$name; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$name ./cmd/runnerkit || exit 1; \
	done
	cd dist && sha256sum runnerkit-* > SHA256SUMS

clean:
	rm -rf dist runnerkit
