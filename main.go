package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
)

func main() {
	addr := envOr("KOBO_ADDR", ":8080")
	token := envOr("KOBO_TOKEN", "")
	epubDir := envOr("KOBO_EPUB_DIR", ".")
	externalURL := envOr("KOBO_EXTERNAL_URL", "")
	debug := os.Getenv("KOBO_DEBUG") != ""

	if token == "" {
		suggested := randomToken()
		slog.Error("KOBO_TOKEN is required",
			"hint", "set KOBO_TOKEN="+suggested+" in your environment or docker-compose.yml")
		os.Exit(1)
	}

	if externalURL == "" {
		ip := detectLANIP()
		port := extractPort(addr)
		if port == "80" {
			externalURL = "http://" + ip
		} else {
			externalURL = fmt.Sprintf("http://%s:%s", ip, port)
		}
		slog.Warn("KOBO_EXTERNAL_URL not set — using auto-detected LAN address (only reliable with host networking)",
			"external_url", externalURL)
	}

	srv := newServer(config{
		token:       token,
		epubDir:     epubDir,
		externalURL: externalURL,
		debug:       debug,
	})

	slog.Info("kobo-sync starting", "addr", addr, "epub_dir", epubDir)
	slog.Info("set in .kobo/Kobo/Kobo eReader.conf under [OneStoreServices]",
		"api_endpoint", externalURL+"/kobo/"+token,
	)

	if err := http.ListenAndServe(addr, srv); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func randomToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "changeme"
	}
	return hex.EncodeToString(b)
}

// virtualIfacePrefixes lists interface name prefixes that are never the right
// choice for a LAN address: Docker bridges, VPN tunnels, VM networks, etc.
var virtualIfacePrefixes = []string{
	"docker", "br-", "veth", "virbr", "virt",
	"tun", "tap", "utun",
	"wg",         // WireGuard
	"tailscale",  // Tailscale
	"vmnet",      // VMware
	"vboxnet",    // VirtualBox
	"lo",
}

func detectLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "YOUR_LAN_IP"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isVirtualIface(iface.Name) {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
				return ip4.String()
			}
		}
	}
	return "YOUR_LAN_IP"
}

func isVirtualIface(name string) bool {
	for _, prefix := range virtualIfacePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func extractPort(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "8080"
	}
	return port
}
