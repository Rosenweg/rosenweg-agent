# Bau aller Plattformen; VERSION aus dem Git-Tag (v0.1.0 -> 0.1.0).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X main.version=$(VERSION)
ZIEL := dist
BAU := CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)'

.PHONY: alle pruefen test
alle:
	mkdir -p $(ZIEL)
	GOOS=windows GOARCH=amd64 $(BAU) -o $(ZIEL)/rosenweg-agent-windows-amd64.exe ./cmd/rosenweg-agent
	GOOS=windows GOARCH=arm64 $(BAU) -o $(ZIEL)/rosenweg-agent-windows-arm64.exe ./cmd/rosenweg-agent
	GOOS=linux GOARCH=amd64 $(BAU) -o $(ZIEL)/rosenweg-agent-linux-amd64 ./cmd/rosenweg-agent
	GOOS=linux GOARCH=arm64 $(BAU) -o $(ZIEL)/rosenweg-agent-linux-arm64 ./cmd/rosenweg-agent
	cd $(ZIEL) && shasum -a 256 rosenweg-agent-* > SHA256SUMS

pruefen:
	test -z "$$(gofmt -l .)"
	GOOS=windows go vet ./...
	GOOS=linux go vet ./...

test:
	go test -count=1 ./...
