.PHONY: proto
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/scheduler.proto

.PHONY: clean
clean:
	rm -f proto/*.pb.go
	rm -rf build

.PHONY: run
run:
	go run main.go

.PHONY: build
build:
	@echo "Building the scheduler binary..."
	@mkdir -p build
	@rm -f build/scheduler
	go build -o build/scheduler main.go

.PHONY: all
all: clean proto build