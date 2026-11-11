BIN_OUTPUT_PATH = bin
MODULE_BINARY = $(BIN_OUTPUT_PATH)/tdk-invensense
LDFLAGS = -s -w

.PHONY: module build lint test clean-all update

module: build
	rm -f $(BIN_OUTPUT_PATH)/module.tar.gz
	tar czf $(BIN_OUTPUT_PATH)/module.tar.gz $(MODULE_BINARY) meta.json

build:
	rm -f $(MODULE_BINARY)
	CGO_ENABLED=0 go build -tags osusergo,netgo -ldflags="$(LDFLAGS)" -o $(MODULE_BINARY) .

lint:
	go mod tidy
	go vet -vettool="$$(go tool -n combined)" ./...
	GOGC=50 go tool golangci-lint run -v --fix --config=./etc/golangci.yaml

test:
	go tool gotestsum --format standard-verbose -- -race -count=1 ./...

clean-all:
	git clean -fxd

update:
	go get go.viam.com/rdk@latest go.viam.com/api@latest
	go mod tidy
