VERSION := 0.4.0
GOBIN   := $(shell go env GOPATH)/bin
LDFLAGS := -X chakram/internal/version.Version=$(VERSION)

.PHONY: build dist dist-web install test clean

build:
	go build -ldflags "$(LDFLAGS)" -o chakram        ./cmd/chakram/
	go build -ldflags "$(LDFLAGS)" -o chakramctl     ./cmd/chakramctl/
	go build -ldflags "$(LDFLAGS)" -o genesis-tool   ./cmd/genesis-tool/
	go build -ldflags "$(LDFLAGS)" -o chakram-node   ./cmd/chakram-node/

# Build the web UI and copy it into cmd/chakram-node/webdist/ for embedding.
dist-web:
	cd web && npm ci && npm run build
	rm -rf cmd/chakram-node/webdist
	cp -r web/dist cmd/chakram-node/webdist

# Release builds: embed web UI, build for all platforms.
dist: dist-web
	mkdir -p dist
	GOOS=linux   GOARCH=amd64 go build -tags webembed -ldflags "$(LDFLAGS)" -o dist/chakram-node-linux-amd64       ./cmd/chakram-node/
	GOOS=linux   GOARCH=arm64 go build -tags webembed -ldflags "$(LDFLAGS)" -o dist/chakram-node-linux-arm64       ./cmd/chakram-node/
	GOOS=darwin  GOARCH=amd64 go build -tags webembed -ldflags "$(LDFLAGS)" -o dist/chakram-node-darwin-amd64      ./cmd/chakram-node/
	GOOS=darwin  GOARCH=arm64 go build -tags webembed -ldflags "$(LDFLAGS)" -o dist/chakram-node-darwin-arm64      ./cmd/chakram-node/
	GOOS=windows GOARCH=amd64 go build -tags webembed -ldflags "$(LDFLAGS)" -o dist/chakram-node-windows-amd64.exe ./cmd/chakram-node/

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/chakramctl/
	go install -ldflags "$(LDFLAGS)" ./cmd/chakram-node/
	@echo "chakramctl + chakram-node $(VERSION) installed to $(GOBIN)"

test:
	go test ./...

clean:
	rm -f chakram chakramctl genesis-tool chakram-node
	rm -rf dist/ cmd/chakram-node/webdist/
