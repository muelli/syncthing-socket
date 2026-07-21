# Use native Just if/else to avoid Windows shell syntax errors
GO := if `[ -f "./local/go/bin/go" ] || [ -f "./local/go/bin/go.exe" ] && echo "1" || echo ""` == "1" { "./local/go/bin/go" } else { "go" }

# Evaluate git commands using a centralized script to avoid discrepancies
VERSION := `sh scripts/version.sh`

# Build the project
build:
    {{GO}} build -ldflags "-X main.Version={{VERSION}}" -o syncthing-socket .

# Run the test suite
test:
    {{GO}} test -v ./...

# Simulate GitHub CI locally using act and podman
ci:
    DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock ~/.local/bin/act
