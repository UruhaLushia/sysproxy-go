//go:build linux

package sysproxy

import (
	"fmt"
	"strings"
)

type Environment struct {
	ctx         *linuxExecContext
	desktop     string
	isKde       bool
	isKde6      bool
	isGnome     bool
	initialized bool
}

func (e *Environment) Init(opt *Options) error {
	if e.initialized {
		return nil
	}

	ctx, err := newLinuxExecContext(opt)
	if err != nil {
		return err
	}

	desktop := ctx.envMap["XDG_CURRENT_DESKTOP"]
	if desktop == "" {
		return fmt.Errorf("XDG_CURRENT_DESKTOP environment variable not set")
	}

	e.ctx = ctx
	e.desktop = desktop
	e.isKde = desktop == "KDE"
	e.isKde6 = e.isKde && ctx.envMap["KDE_SESSION_VERSION"] == "6"
	e.isGnome = strings.Contains(desktop, "GNOME") || desktop == "Unity" ||
		desktop == "X-Cinnamon" || desktop == "niri"
	e.initialized = true

	return nil
}

func DisableProxy(opt *Options) error {
	e := &Environment{}
	if err := e.Init(opt); err != nil {
		return err
	}

	switch {
	case e.isKde:
		return clearKDEProxy(e)
	case e.isGnome:
		return clearGnomeProxy(e)
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
		config, err := QueryProxySettings(opt)
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
	if err := e.Init(opt); err != nil {
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
		return setKDEProxy(e, config)
	case e.isGnome:
		return setGnomeProxy(e, config)
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
	if err := e.Init(opt); err != nil {
		return err
	}

	if pacUrl == "" {
		currentConfig, err := QueryProxySettings(opt)
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
		return setKDEPac(e, config)
	case e.isGnome:
		return setGnomePac(e, config)
	default:
		return fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func QueryProxySettings(opt *Options) (*ProxyConfig, error) {
	e := &Environment{}
	if err := e.Init(opt); err != nil {
		return nil, err
	}

	switch {
	case e.isKde:
		return queryKDESettings(e)
	case e.isGnome:
		return queryGnomeSettings(e)
	default:
		return nil, fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func queryGnomeSettings(e *Environment) (*ProxyConfig, error) {
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
		output, err := execAsCurrentUser(e.ctx, "gsettings", append([]string{"get"}, strings.Split(key.path, " ")...)...).Output()
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

func setGnomeProxy(e *Environment, config *ProxyConfig) error {
	if err := execGsettings(e, "org.gnome.system.proxy", "mode", "manual"); err != nil {
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
			if err := execGsettings(e, fmt.Sprintf("org.gnome.system.proxy.%s", proxyType), "host", addr.host); err != nil {
				return err
			}
			if err := execGsettings(e, fmt.Sprintf("org.gnome.system.proxy.%s", proxyType), "port", addr.port); err != nil {
				return err
			}
		}
	}

	if config.Proxy.Bypass != "" {
		bypassList := fmt.Sprintf("['%s']", strings.Join(strings.Split(config.Proxy.Bypass, ","), "','"))
		if err := execGsettings(e, "org.gnome.system.proxy", "ignore-hosts", bypassList); err != nil {
			return err
		}
	}

	return execGsettings(e, "org.gnome.system.proxy", "use-same-proxy", fmt.Sprintf("%v", config.Proxy.SameForAll))
}

func setGnomePac(e *Environment, config *ProxyConfig) error {
	if err := execGsettings(e, "org.gnome.system.proxy", "mode", "auto"); err != nil {
		return err
	}
	return execGsettings(e, "org.gnome.system.proxy", "autoconfig-url", config.PAC.URL)
}

func clearGnomeProxy(e *Environment) error {
	return execGsettings(e, "org.gnome.system.proxy", "mode", "none")
}

func execGsettings(e *Environment, schema, key, value string) error {
	return execAsCurrentUser(e.ctx, "gsettings", "set", schema, key, value).Run()
}

func queryKDESettings(e *Environment) (*ProxyConfig, error) {
	cmd := "kreadconfig5"
	if e.isKde6 {
		cmd = "kreadconfig6"
	}

	group := "Proxy Settings"
	if !e.isKde6 {
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
		output, err := execAsCurrentUser(e.ctx, cmd, "--file", "kioslaverc", "--group", group, "--key", key).Output()
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

func setKDEProxy(e *Environment, config *ProxyConfig) error {
	cmd := "kwriteconfig5"
	if e.isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !e.isKde6 {
		group = "Proxy"
	}

	if err := execKDEConfig(e, cmd, "ProxyType", "1", group); err != nil {
		return err
	}

	servers := map[string]string{
		"httpProxy":  config.Proxy.Servers["http_server"],
		"httpsProxy": config.Proxy.Servers["https_server"],
		"socksProxy": config.Proxy.Servers["socks_server"],
		"ftpProxy":   config.Proxy.Servers["ftp_server"],
	}

	for key, value := range servers {
		if err := execKDEConfig(e, cmd, key, value, group); err != nil {
			return err
		}
	}

	if err := execKDEConfig(e, cmd, "NoProxyFor", config.Proxy.Bypass, group); err != nil {
		return err
	}

	sameProxy := "false"
	if config.Proxy.SameForAll {
		sameProxy = "true"
	}
	return execKDEConfig(e, cmd, "UseSameProxy", sameProxy, group)
}

func setKDEPac(e *Environment, config *ProxyConfig) error {
	cmd := "kwriteconfig5"
	if e.isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !e.isKde6 {
		group = "Proxy"
	}

	if err := execKDEConfig(e, cmd, "ProxyType", "2", group); err != nil {
		return err
	}

	return execKDEConfig(e, cmd, "Proxy Config Script", config.PAC.URL, group)
}

func clearKDEProxy(e *Environment) error {
	cmd := "kwriteconfig5"
	if e.isKde6 {
		cmd = "kwriteconfig6"
	}

	group := "Proxy Settings"
	if !e.isKde6 {
		group = "Proxy"
	}

	return execKDEConfig(e, cmd, "ProxyType", "0", group)
}

func execKDEConfig(e *Environment, cmd, key, value, group string) error {
	args := []string{"--file", "kioslaverc", "--group", group, "--key", key, value}
	return execAsCurrentUser(e.ctx, cmd, args...).Run()
}
