# Use native Just if/else to avoid Windows shell syntax errors
GO := if `[ -f "./local/go/bin/go" ] || [ -f "./local/go/bin/go.exe" ] && echo "1" || echo ""` == "1" { "./local/go/bin/go" } else { "go" }

# Evaluate git commands using a centralized script to avoid discrepancies
VERSION := `sh scripts/version.sh`

PREFIX := "/usr/local"
DESTDIR := ""

# Build the project. The root package is the `socket` library; the binary is in ./cmd.
build:
    {{GO}} build -ldflags "-X syncthing-socket.Version={{VERSION}}" -o syncthing-socket ./cmd/syncthing-socket

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

# Install the initramfs LUKS integration. Deliberately separate from `install`: it only
# makes sense on Debian/Ubuntu with initramfs-tools, and it rewrites your boot path.
install-contrib:
    install -Dm755 contrib/initramfs-luks/syncthing-socket-hook {{DESTDIR}}/etc/initramfs-tools/hooks/syncthing-socket
    install -Dm755 contrib/initramfs-luks/syncthing-socket-initramfs-top {{DESTDIR}}/etc/initramfs-tools/scripts/local-top/syncthing-socket
    install -Dm755 contrib/initramfs-luks/syncthing-socket-initramfs-bottom {{DESTDIR}}/etc/initramfs-tools/scripts/local-bottom/syncthing-socket
    install -Dm755 contrib/initramfs-luks/syncthing-luks-bind {{DESTDIR}}{{PREFIX}}/sbin/syncthing-luks-bind
    install -Dm755 contrib/initramfs-luks/syncthing-luks-setup {{DESTDIR}}{{PREFIX}}/sbin/syncthing-luks-setup

# Run the test suite
test:
    {{GO}} test -v ./...

# Simulate GitHub CI locally using act and podman
ci:
    DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock ~/.local/bin/act

# Build the Android app. Pass an application id and label to produce a variant that
# installs alongside the default one, for unlocking a second computer:
#   just android-apk com.github.muelli.syncthingsocket.office "LUKS Office"
android-apk id="com.github.muelli.syncthingsocket" label="Syncthing LUKS":
    cd android && ./gradlew assembleRelease \
        -PsyncthingSocket.appId="{{id}}" \
        -PsyncthingSocket.appLabel="{{label}}"
    @echo "APK: android/app/build/outputs/apk/release/"
