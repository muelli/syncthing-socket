# Build the project
build:
    go build -ldflags "-X main.Version=$(git rev-list --count HEAD)" -o syncthing-socket .

# Run the test suite
test:
    go test -v ./...
