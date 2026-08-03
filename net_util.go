package socket

import "strings"

// parseNetworkAndAddr parses an address string into a network type ("tcp" or "unix") and the address/path.
// It supports explicit prefixes ("unix://", "unix:", "tcp://", "tcp:") and smart auto-detection
// for UNIX domain socket paths (starting with "/", "./", "../", ending in ".sock", or containing no colon).
func parseNetworkAndAddr(addr string) (string, string) {
	if strings.HasPrefix(addr, "unix://") {
		return "unix", strings.TrimPrefix(addr, "unix://")
	}
	if strings.HasPrefix(addr, "unix:") {
		return "unix", strings.TrimPrefix(addr, "unix:")
	}
	if strings.HasPrefix(addr, "tcp://") {
		return "tcp", strings.TrimPrefix(addr, "tcp://")
	}
	if strings.HasPrefix(addr, "tcp:") {
		return "tcp", strings.TrimPrefix(addr, "tcp:")
	}
	if strings.HasPrefix(addr, "/") || strings.HasPrefix(addr, "./") || strings.HasPrefix(addr, "../") || strings.HasSuffix(addr, ".sock") || !strings.Contains(addr, ":") {
		return "unix", addr
	}
	return "tcp", addr
}
