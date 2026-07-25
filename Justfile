# Use native Just if/else to avoid Windows shell syntax errors
GO := if `[ -f "./local/go/bin/go" ] || [ -f "./local/go/bin/go.exe" ] && echo "1" || echo ""` == "1" { "./local/go/bin/go" } else { "go" }

# Evaluate git commands using a centralized script to avoid discrepancies
VERSION := `sh scripts/version.sh`

PREFIX := "/usr/local"
DESTDIR := ""

# Build the project
build:
    {{GO}} build -ldflags "-X main.Version={{VERSION}}" -o syncthing-socket .

# Install the binary, man page, completions, and systemd service
install: build
    install -Dm755 syncthing-socket {{DESTDIR}}{{PREFIX}}/bin/syncthing-socket
    install -Dm644 syncthing-socket.1 {{DESTDIR}}{{PREFIX}}/share/man/man1/syncthing-socket.1
    # Pre-generate and install shell completions (bash, zsh, fish)
    ./syncthing-socket completion bash > syncthing-socket.bash
    install -Dm644 syncthing-socket.bash {{DESTDIR}}{{PREFIX}}/share/bash-completion/completions/syncthing-socket
    ./syncthing-socket completion zsh > syncthing-socket.zsh
    install -Dm644 syncthing-socket.zsh {{DESTDIR}}{{PREFIX}}/share/zsh/site-functions/_syncthing-socket
    ./syncthing-socket completion fish > syncthing-socket.fish
    install -Dm644 syncthing-socket.fish {{DESTDIR}}{{PREFIX}}/share/fish/vendor_completions.d/syncthing-socket.fish
    rm syncthing-socket.bash syncthing-socket.zsh syncthing-socket.fish
    # Generate and install systemd service with softcoded PREFIX
    sed -e "s|@PREFIX@|{{PREFIX}}|g" contrib/syncthing-socket.service.in > syncthing-socket.service
    install -Dm644 syncthing-socket.service {{DESTDIR}}{{PREFIX}}/lib/systemd/system/syncthing-socket.service
    rm syncthing-socket.service

# Run the test suite
test:
    {{GO}} test -v ./...

# Simulate GitHub CI locally using act and podman
ci:
    DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock ~/.local/bin/act
