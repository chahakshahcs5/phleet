# phleet — convenience targets for Unix-like hosts.
# On Windows use scripts/build.ps1 instead.

BIN := dist
VERSION := 0.10.0

.PHONY: all web build cli agent test clean

all: build agent

web:
	cd web && npm ci --no-audit --no-fund && npm run typecheck && npm run build

build: web
	go build -trimpath -o $(BIN)/phleetd ./cmd/phleetd
	go build -trimpath -o $(BIN)/phleet ./cmd/phleet

cli:
	go build -trimpath -o $(BIN)/phleet ./cmd/phleet

agent:
	mkdir -p $(BIN)/bin
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o $(BIN)/bin/phleet-agent-linux-arm64 ./cmd/phleet-agent
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o $(BIN)/bin/phleet-agent-linux-amd64 ./cmd/phleet-agent
	cp scripts/bootstrap-termux.sh $(BIN)/bin/bootstrap.sh
	cp scripts/bootstrap-android-root.sh $(BIN)/bin/bootstrap-root.sh
	cp scripts/bootstrap-debian-chroot.sh $(BIN)/bin/bootstrap-debian.sh

test:
	mkdir -p $(BIN)/gotmp
	GOTMPDIR=$(CURDIR)/$(BIN)/gotmp go test ./...

clean:
	rm -rf $(BIN) .tmp
