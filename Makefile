build:
	go build -o bin/cfvpnctl ./cmd/cfvpnctl

test:
	go test ./...

all: test build
