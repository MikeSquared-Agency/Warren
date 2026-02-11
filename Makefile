.PHONY: build run clean

build:
	go build -o bin/orchestrator ./cmd/orchestrator

run: build
	./bin/orchestrator --config configs/orchestrator.yaml

clean:
	rm -rf bin/
