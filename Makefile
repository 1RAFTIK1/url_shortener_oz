BINARY := shortener
PKG    := ./cmd/shortener

.PHONY: build run test race cover lint vuln tidy check clean

build:
	go build -trimpath -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG) -storage=memory

test:
	go test ./... -count=1

race:
	go test ./... -race -count=1

cover:
	go test ./... -coverprofile=coverage.out -covermode=atomic -count=1
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

tidy:
	go mod tidy

check: race lint vuln

clean:
	rm -rf bin coverage.out

fmt:
	gofmt -w .
	go mod tidy