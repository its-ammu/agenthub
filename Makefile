.PHONY: build install test clean

build:
	go build -o agenthub-server ./cmd/agenthub-server
	go build -o ah ./cmd/ah

install:
	./install.sh

test:
	go vet ./...
	go test ./...

clean:
	rm -f agenthub-server ah
