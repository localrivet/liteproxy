.PHONY: build run clean test

build:
	go build -o liteproxy .

run: build
	LITEPROXY_HTTPS_ENABLED=false ./liteproxy

clean:
	rm -f liteproxy

test:
	go test ./...
