GO := `[ -x "./local/go/bin/go" ] && echo "./local/go/bin/go" || echo "go"`
VERSION := `echo "$(git describe --exact-match --tags HEAD 2>/dev/null || git rev-list --count HEAD)$([ -z \"$(git status --porcelain)\" ] || echo \"-dirty\")"`

# Build the project
build:
    {{GO}} build -ldflags "-X main.Version={{VERSION}}" -o syncthing-socket .

# Run the test suite
test:
    {{GO}} test -v ./...

# Simulate GitHub CI locally using act and podman
ci:
    DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock ~/.local/bin/act
