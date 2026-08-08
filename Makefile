.PHONY: build help test vet site clean

build:
	go build -o angr-memories ./cmd/angr-memories

help:
	@echo "angr-memories"
	@echo ""
	@echo "Targets:"
	@echo "  make build   Build the angr-memories binary"
	@echo "  make test    Run go test ./..."
	@echo "  make vet     Run go vet ./..."
	@echo "  make site    Render the static site into _site/"
	@echo "  make clean   Remove the built binary and _site/"

test:
	go test ./...

vet:
	go vet ./...

site:
	go run ./cmd/angr-memories site --memories memories.jsonl --out _site
	@echo "Wrote _site/ ($$(find _site -type f | wc -l) files)"

clean:
	rm -rf angr-memories _site
