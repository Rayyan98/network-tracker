.PHONY: build clean test run

build:
	CGO_ENABLED=1 go build -o bin/network-tracker .

run: build
	sudo ./bin/network-tracker $(ARGS)

test:
	go test ./...

clean:
	rm -rf bin/
