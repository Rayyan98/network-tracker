DB_PATH := $(HOME)/.network-tracker/data.db

.PHONY: build test clean run reset resetdb stop

build:
	CGO_ENABLED=1 go build -o bin/network-tracker .

test:
	go test ./...

run: build
	sudo ./bin/network-tracker $(ARGS)

# Delete the database (needs sudo since daemon creates it as root)
resetdb:
	sudo rm -f $(DB_PATH) $(DB_PATH)-wal $(DB_PATH)-shm
	@echo "Database deleted"

# Delete DB and start fresh
reset: build resetdb
	sudo ./bin/network-tracker $(ARGS)

# Stop the running daemon
stop:
	@sudo pkill -f 'network-tracker' 2>/dev/null && echo "Stopped" || echo "Not running"

# Stop, delete DB, rebuild, start
restart: stop build resetdb
	sudo ./bin/network-tracker $(ARGS)

clean:
	rm -rf bin/
