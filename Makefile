.PHONY: build test vet clean install cross-build

BINARY := dmr-plugin-cron

# Default build (pure Go; sqlite via modernc.org/sqlite)
# tidy first: upstream dmr/go-plugin bumps can require an updated go.sum
build:
	go mod tidy
	go build -o $(BINARY) .

test:
	go test ./... -count=1

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
	rm -f $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
	rm -f $(BINARY)-darwin-amd64 $(BINARY)-darwin-arm64

install: build
	mkdir -p ~/.dmr/plugins
	cp $(BINARY) ~/.dmr/plugins/
	@echo "Installed to ~/.dmr/plugins/$(BINARY)"

cross-build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BINARY)-linux-amd64 .
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o $(BINARY)-linux-arm64 .
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o $(BINARY)-darwin-amd64 .
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o $(BINARY)-darwin-arm64 .
