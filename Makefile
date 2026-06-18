# Talk to your GitHub stars — build automation

BINARY := ttygs
CMD := ./cmd/ttygs

export CGO_ENABLED := 0

.PHONY: all build test clean run

all: build

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

clean:
	rm -f $(BINARY)
	go clean -cache

run: build
	./$(BINARY)
