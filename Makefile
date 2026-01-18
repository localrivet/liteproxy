.PHONY: build run clean test coverage

build:
	go build -o liteproxy .

run: build
	LITEPROXY_HTTPS_ENABLED=false ./liteproxy

clean:
	rm -f liteproxy coverage.out

test:
	go test ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
