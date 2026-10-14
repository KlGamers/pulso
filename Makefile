.PHONY: build test lint run

build:
	go build -trimpath -ldflags="-s -w" -o pulso ./cmd/pulso

test:
	go test -race -count=1 ./...

lint:
	gofmt -l . && go vet ./...

run: build
	./pulso -c pulso.json
