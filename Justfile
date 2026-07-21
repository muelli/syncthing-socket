GO := `[ -x "./local/go/bin/go" ] && echo "./local/go/bin/go" || echo "go"`

# Build the project
build:
    {{GO}} build -ldflags "-X main.Version=$(git rev-list --count HEAD)" -o syncthing-socket .

# Run the test suite
test:
    {{GO}} test -v ./...
