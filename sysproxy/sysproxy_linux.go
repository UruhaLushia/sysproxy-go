//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type Environment struct {
	desktop     string
	isKde       bool
	isKde6      bool
	isGnome     bool
	initialized bool
}

func (e *Environment) Init() error {
	if e.initialized {
		return nil
	}

	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	if desktop == "" {
		return fmt.Errorf("XDG_CURRENT_DESKTOP environment variable not set")
	}

	e.desktop = desktop
	e.isKde = desktop == "KDE"
	e.isKde6 = e.isKde && os.Getenv("KDE_SESSION_VERSION") == "6"
	e.isGnome = strings.Contains(desktop, "GNOME") || desktop == "Unity" ||
		desktop == "X-Cinnamon" || desktop == "niri"
	e.initialized = true

	return nil
}

func DisableProxy(_ *Options) error {
	e := &Environment{}
	if err := e.Init(); err != nil {
		return err
	}

	switch {
	case e.isKde:
		return clearKDEProxy(e.isKde6)
	case e.isGnome:
		return clearGnomeProxy()
	default:
		return fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func SetProxy(opt *Options) error {
	proxy := ""
	bypass := ""
	if opt != nil {
		proxy = opt.Proxy
		bypass = opt.Bypass
	}
	if proxy == "" || bypass == "" {
		config, err := QueryProxySettings(nil)
		if err != nil {
			return err
		}

		if proxy == "" {
			proxy = config.Proxy.Servers["http_server"]
		}
		if bypass == "" {
			bypass = config.Proxy.Bypass
		}
	}
	e := &Environment{}
	if err := e.Init(); err != nil {
		return err
	}

	config := &ProxyConfig{}
	config.Proxy.Enable = true
	config.Proxy.SameForAll = true
	config.Proxy.Servers = map[string]string{
		"http_server":  proxy,
		"https_server": proxy,
		"socks_server": proxy,
	}
	config.Proxy.Bypass = bypass

	switch {
	case e.isKde:
		return setKDEProxy(config, e.isKde6)
	case e.isGnome:
		return setGnomeProxy(config)
	default:
		return fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func SetPac(opt *Options) error {
	pacUrl := ""
	if opt != nil {
		pacUrl = opt.PACURL
	}
	e := &Environment{}
	if err := e.Init(); err != nil {
		return err
	}

	if pacUrl == "" {
		currentConfig, err := QueryProxySettings(nil)
		if err != nil {
			return err
		}
		pacUrl = currentConfig.PAC.URL
	}

	config := &ProxyConfig{}
	config.PAC.Enable = true
	config.PAC.URL = pacUrl

	switch {
	case e.isKde:
		return setKDEPac(config, e.isKde6)
	case e.isGnome:
		return setGnomePac(config)
	default:
		return fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func QueryProxySettings(_ *Options) (*ProxyConfig, error) {
	e := &Environment{}
	if err := e.Init(); err != nil {
		return nil, err
	}

	switch {
	case e.isKde:
		return queryKDESettings(e.isKde6)
	case e.isGnome:
		return queryGnomeSettings()
	default:
		return nil, fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func execAsCurrentUser(name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	if os.Geteuid() == 0 {
		fmt.Println(os.Getuid(), os.Getgid())
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{
				Uid: uint32(os.Getuid()),
				Gid: uint32(os.Getgid()),
			},
		}
	}
	return cmd
}

func queryGnomeSettings() (*ProxyConfig, error) {
	settings := map[string]string{}
	keys := []struct {
		name, path string
	}{
		{"mode", "org.gnome.system.proxy mode"},
		{"ignore-hosts", "org.gnome.system.proxy ignore-hosts"},
		{"autoconfig-url", "org.gnome.system.proxy autoconfig-url"},
		{"use-same-proxy", "org.gnome.system.proxy use-same-proxy"},
		{"http_host", "org.gnome.system.proxy.http host"},
		{"http_port", "org.gnome.system.proxy.http port"},
		{"https_host", "org.gnome.system.proxy.https host"},
		{"https_port", "org.gnome.system.proxy.https port"},
		{"ftp_host", "org.gnome.system.proxy.ftp host"},
		{"ftp_port", "org.gnome.system.proxy.ftp port"},
		{"socks_host", "org.gnome.system.proxy.socks host"},
		{"socks_port", "org.gnome.system.proxy.socks port"},
	}

	for _, key := range keys {
		output, err := execAsCurrentUser("gsettings", append([]string{"get"}, strings.Split(key.path, " ")...)...).Output()
		if err != nil {
			return nil, fmt.Errorf("无法读取 %s 的 GNOME 配置：%v", key.name, err)
		}
		settings[key.name] = string(output)
	}

	config := &ProxyConfig{}
	config.Proxy.Enable = cleanOutput(settings["mode"]) == "manual"
	config.Proxy.SameForAll = cleanOutput(settings["use-same-proxy"]) == "true"
	config.Proxy.Servers = map[string]string{
		"http_server":  FormatServer(settings["http_host"], settings["http_port"]),
		"https_server": FormatServer(settings["https_host"], settings["https_port"]),
		"socks_server": FormatServer(settings["socks_host"], settings["socks_port"]),
		"ftp_server":   FormatServer(settings["ftp_host"], settings["ftp_port"]),
	}

	bypassList := cleanOutput(settings["ignore-hosts"])
	if bypassList != "" {
		items := strings.Split(bypassList, ",")
		for i, item := range items {
			items[i] = cleanOutput(item)
		}
		config.Proxy.Bypass = strings.Join(items, ",")
	}

	config.PAC.Enable = cleanOutput(settings["mode"]) == "auto"
	config.PAC.URL = cleanOutput(settings["autoconfig-url"])

	return config, nil
}

func setGnomeProxy(config *ProxyConfig) error {
	if err := execGsettings("org.gnome.system.proxy", "mode", "manual"); err != nil {
		return err
	}

	proxyTypes := map[string]struct{ host, port string }{
		"http":  ParseServerString(config.Proxy.Servers["http_server"]),
		"https": ParseServerString(config.Proxy.Servers["https_server"]),
		"ftp":   ParseServerString(config.Proxy.Servers["ftp_server"]),
		"socks": ParseServerString(config.Proxy.Servers["socks_server"]),
	}

	for proxyType, addr := range proxyTypes {
		fmt.Println(proxyType, addr)
		if addr.host != "" {
			if err := execGsettings(fmt.Sprintf("org.gnome.system.proxy.%s", proxyType), "host", addr.host); err != nil {
				return err
			}
			if err := execGsettings(fmt.Sprintf("org.gnome.system.proxy.%s", proxyType), "port", addr.port); err != nil {
				return err
			}
		}
	}

	if config.Proxy.Bypass != "" {
		bypassList := fmt.Sprintf("['%s']", strings.Join(strings.Split(config.Proxy.Bypass, ","), "','"))
		if err := execGsettings("org.gnome.system.proxy", "ignore-hosts", bypassList); err != nil {
			return err
		}
	}

	return execGsettings("org.gnome.system.proxy", "use-same-proxy", fmt.Sprintf("%v", config.Proxy.SameForAll))
}

func setGnomePac(config *ProxyConfig) error {
	if err := execGsettings("org.gnome.system.proxy", "mode", "auto"); err != nil {
		return err
	}
	return execGsettings("org.gnome.system.proxy", "autoconfig-url", config.PAC.URL)
}

func clearGnomeProxy() error {
	return execGsettings("org.gnome.system.proxy", "mode", "none")
}

func execGsettings(schema, key, value string) error {
	return execAsCurrentUser("gsettings", "set", schema, key, value).Run()
}

func queryKDESettings(isKde6 bool) (*ProxyConfig, error) {
	cmd := "kreadconfig5"
	if isKde6 {
		cmd = "kreadconfig6"
	}

	group := "Proxy Settings"
	if !isKde6 {
		group = "Proxy"
	}

	keys := map[string]string{
		"ProxyType":           "",
		"httpProxy":           "",
		"httpsProxy":          "",
		"socksProxy":          "",
		"ftpProxy":            "",
		"NoProxyFor":          "",
		"Proxy Config Script": "",
		"UseSameProxy":        "",
	}

	for key := range keys {
		output, err := execAsCurrentUser(cmd, "--file", "kioslaverc", "--group", group, "--key", key).Output()
		if err != nil {
			return nil, fmt.Errorf("无法读取 %s 的 KDE 配置：%v", key, err)
		}
		keys[key] = cleanOutput(string(output))
	}

	config := &ProxyConfig{}
	config.Proxy.Enable = keys["ProxyType"] == "1"
	config.Proxy.SameForAll = keys["UseSameProxy"] == "true"
	config.Proxy.Servers = map[string]string{
		"http_server":  strings.ReplaceAll(keys["httpProxy"], " ", ":"),
		"https_server": strings.ReplaceAll(keys["httpsProxy"], " ", ":"),
		"socks_server": strings.ReplaceAll(keys["socksProxy"], " ", ":"),
		"ftp_server":   strings.ReplaceAll(keys["ftpProxy"], " ", ":"),
	}

	for key, value := range config.Proxy.Servers {
		if value == "" || value == "0" {
			config.Proxy.Servers[key] = ""
		}
	}

	config.Proxy.Bypass = keys["NoProxyFor"]
	config.PAC.Enable = keys["ProxyType"] == "2"
	config.PAC.URL = keys["Proxy Config Script"]

	return config, nil
}

func setKDEProxy(config *ProxyConfig, isKde6 bool) error {
	cmd := "kwriteconfig5"
	if isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !isKde6 {
		group = "Proxy"
	}

	if err := execKDEConfig(cmd, "ProxyType", "1", group); err != nil {
		return err
	}

	servers := map[string]string{
		"httpProxy":  config.Proxy.Servers["http_server"],
		"httpsProxy": config.Proxy.Servers["https_server"],
		"socksProxy": config.Proxy.Servers["socks_server"],
		"ftpProxy":   config.Proxy.Servers["ftp_server"],
	}

	for key, value := range servers {
		if err := execKDEConfig(cmd, key, value, group); err != nil {
			return err
		}
	}

	if err := execKDEConfig(cmd, "NoProxyFor", config.Proxy.Bypass, group); err != nil {
		return err
	}

	sameProxy := "false"
	if config.Proxy.SameForAll {
		sameProxy = "true"
	}
	return execKDEConfig(cmd, "UseSameProxy", sameProxy, group)
}

func setKDEPac(config *ProxyConfig, isKde6 bool) error {
	cmd := "kwriteconfig5"
	if isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !isKde6 {
		group = "Proxy"
	}

	if err := execKDEConfig(cmd, "ProxyType", "2", group); err != nil {
		return err
	}

	return execKDEConfig(cmd, "Proxy Config Script", config.PAC.URL, group)
}

func clearKDEProxy(isKde6 bool) error {
	cmd := "kwriteconfig5"
	if isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !isKde6 {
		group = "Proxy"
	}

	return execKDEConfig(cmd, "ProxyType", "0", group)
}

func execKDEConfig(cmd, key, value, group string) error {
	args := []string{"--file", "kioslaverc", "--group", group, "--key", key, value}
	return execAsCurrentUser(cmd, args...).Run()
}
