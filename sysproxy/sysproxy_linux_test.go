//go:build linux

package sysproxy

import "testing"

func TestFormatKDEProxyServer(t *testing.T) {
	tests := []struct {
		name   string
		server string
		scheme string
		want   string
	}{
		{name: "http", server: "127.0.0.1:7890", scheme: "http", want: "http://127.0.0.1 7890"},
		{name: "socks", server: "127.0.0.1:7890", scheme: "socks", want: "socks://127.0.0.1 7890"},
		{name: "existing scheme", server: "http://127.0.0.1:7890", scheme: "http", want: "http://127.0.0.1 7890"},
		{name: "ipv6", server: "[::1]:7890", scheme: "http", want: "http://[::1] 7890"},
		{name: "empty", server: "", scheme: "http", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatKDEProxyServer(tt.server, tt.scheme); got != tt.want {
				t.Fatalf("formatKDEProxyServer(%q, %q) = %q, want %q", tt.server, tt.scheme, got, tt.want)
			}
		})
	}
}

func TestParseKDEProxyServer(t *testing.T) {
	tests := []struct {
		name   string
		server string
		want   string
	}{
		{name: "http", server: "http://127.0.0.1 7890", want: "127.0.0.1:7890"},
		{name: "socks", server: "socks://127.0.0.1 7890", want: "127.0.0.1:7890"},
		{name: "ipv6", server: "http://[::1] 7890", want: "[::1]:7890"},
		{name: "legacy", server: "127.0.0.1:7890", want: "127.0.0.1:7890"},
		{name: "empty", server: "0", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseKDEProxyServer(tt.server); got != tt.want {
				t.Fatalf("parseKDEProxyServer(%q) = %q, want %q", tt.server, got, tt.want)
			}
		})
	}
}
