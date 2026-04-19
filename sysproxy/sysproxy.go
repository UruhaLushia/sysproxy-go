package sysproxy

import (
	"fmt"
	"strings"
)

type ProxyConfig struct {
	Proxy struct {
		Enable     bool              `json:"enable"`
		SameForAll bool              `json:"same_for_all"`
		Servers    map[string]string `json:"servers"`
		Bypass     string            `json:"bypass"`
	} `json:"proxy"`
	PAC struct {
		Enable bool   `json:"enable"`
		URL    string `json:"url"`
	} `json:"pac"`
}

type serverAddr struct {
	host string
	port string
}

func Bool(v bool) *bool {
	return &v
}

func FormatServer(host, port string) string {
	host = cleanOutput(host)
	port = cleanOutput(port)

	if host == "" || port == "" || port == "0" {
		return ""
	}

	return fmt.Sprintf("%s:%s", host, port)
}

func cleanOutput(s string) string {
	s = strings.Trim(s, "'[]\" \n")
	return strings.TrimSpace(s)
}

func ParseServerString(server string) serverAddr {
	if server == "" {
		return serverAddr{}
	}
	lastIndex := strings.LastIndex(server, ":")
	if lastIndex == -1 {
		return serverAddr{}
	}
	return serverAddr{
		host: server[:lastIndex],
		port: server[lastIndex+1:],
	}
}
