# Build the project
build:
    go build -o syncthing-socket .

# Run the test suite
test:
    go test -v ./...
