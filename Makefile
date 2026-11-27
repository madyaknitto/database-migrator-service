.PHONY: test vet build build-linux validate clean

BINARY_NAME=pg-migrate
DIST_DIR=dist

vet:
	go vet ./...

test:
	go test -v ./...

build:
	CGO_ENABLED=0 go build -trimpath -o $(DIST_DIR)/$(BINARY_NAME) ./cmd/pg-migrate

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd/pg-migrate

validate:
	go run ./cmd/pg-migrate validate --config=databases.yaml --db=all

clean:
	rm -rf $(DIST_DIR) bin
