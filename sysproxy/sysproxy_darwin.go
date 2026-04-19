//go:build darwin

package sysproxy

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

func DisableProxy(opt *Options) error {
	services, err := getTargetServices(opt)
	if err != nil {
		return err
	}
	commands := [][]string{
		{"-setautoproxystate", "off"},
		{"-setproxyautodiscovery", "off"},
		{"-setwebproxystate", "off"},
		{"-setsecurewebproxystate", "off"},
		{"-setsocksfirewallproxystate", "off"},
	}

	return applyNetworkServices(services, commands, resolveConcurrentApply(opt))
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

	addr := ParseServerString(proxy)
	if addr.host == "" || addr.port == "" {
		return fmt.Errorf("invalid proxy address: %s", proxy)
	}

	services, err := getTargetServices(opt)
	if err != nil {
		return err
	}

	commands := [][]string{
		{"-setautoproxystate", "off"},
		{"-setproxyautodiscovery", "off"},
		{"-setwebproxy", addr.host, addr.port},
		{"-setsecurewebproxy", addr.host, addr.port},
		{"-setsocksfirewallproxy", addr.host, addr.port},
		append([]string{"-setproxybypassdomains"}, strings.Split(bypass, ",")...),
	}

	return applyNetworkServices(services, commands, resolveConcurrentApply(opt))
}

func SetPac(opt *Options) error {
	pacUrl := ""
	if opt != nil {
		pacUrl = opt.PACURL
	}
	if pacUrl == "" {
		config, err := QueryProxySettings(opt)
		if err != nil {
			return err
		}
		pacUrl = config.PAC.URL
	}

	services, err := getTargetServices(opt)
	if err != nil {
		return err
	}

	commands := [][]string{
		{"-setwebproxystate", "off"},
		{"-setsecurewebproxystate", "off"},
		{"-setsocksfirewallproxystate", "off"},
		{"-setautoproxyurl", pacUrl},
		{"-setautoproxystate", "on"},
		{"-setproxyautodiscovery", "on"},
	}

	return applyNetworkServices(services, commands, resolveConcurrentApply(opt))
}

func QueryProxySettings(opt *Options) (*ProxyConfig, error) {
	services, err := getQueryServices(opt)
	if err != nil {
		return nil, err
	}

	service := services[0]
	config := &ProxyConfig{}
	config.Proxy.Servers = make(map[string]string)

	output, err := exec.Command("networksetup", "-getautoproxyurl", service).Output()
	if err == nil && strings.Contains(string(output), "Enabled: Yes") {
		config.PAC.Enable = true
		lines := strings.SplitSeq(string(output), "\n")
		for line := range lines {
			if strings.HasPrefix(line, "URL: ") {
				config.PAC.URL = strings.TrimPrefix(line, "URL: ")
				break
			}
		}
	}

	if enabled, host, port := parseProxy(exec.Command("networksetup", "-getwebproxy", service)); enabled {
		config.Proxy.Enable = true
		if addr := FormatServer(host, port); addr != "" {
			config.Proxy.Servers["http_server"] = addr
		}
	}

	if enabled, host, port := parseProxy(exec.Command("networksetup", "-getsecurewebproxy", service)); enabled {
		config.Proxy.Enable = true
		if addr := FormatServer(host, port); addr != "" {
			config.Proxy.Servers["https_server"] = addr
		}
	}

	if enabled, host, port := parseProxy(exec.Command("networksetup", "-getsocksfirewallproxy", service)); enabled {
		config.Proxy.Enable = true
		if addr := FormatServer(host, port); addr != "" {
			config.Proxy.Servers["socks_server"] = addr
		}
	}

	if output, err := exec.Command("networksetup", "-getproxybypassdomains", service).Output(); err == nil {
		bypass := strings.ReplaceAll(strings.TrimSpace(string(output)), "\n", ",")
		if bypass != "" {
			config.Proxy.Bypass = bypass
		}
	}

	return config, nil
}

func getTargetServices(opt *Options) ([]string, error) {
	if opt != nil && opt.Device != "" {
		return []string{opt.Device}, nil
	}
	onlyActive := false
	if opt != nil {
		onlyActive = opt.OnlyActiveDevice
	}
	return getNetworkServices(onlyActive)
}

func getQueryServices(opt *Options) ([]string, error) {
	if opt != nil && opt.Device != "" {
		return []string{opt.Device}, nil
	}
	if opt != nil {
		return getNetworkServices(opt.OnlyActiveDevice)
	}

	services, err := getNetworkServices(true)
	if err == nil {
		return services, nil
	}
	return getNetworkServices(false)
}

func getNetworkServices(onlyActiveDevice bool) ([]string, error) {
	var (
		ifaces []net.Interface
		err    error
	)
	if onlyActiveDevice {
		ifaces, err = net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("无法获取网络接口：%w", err)
		}
	}

	cmd := exec.Command("networksetup", "-listnetworkserviceorder")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("无法执行 networksetup 命令：%w", err)
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("networksetup 命令没有输出")
	}

	ordinalRegex := regexp.MustCompile(`^\(\d+\)\s*(.+)$`)
	deviceRegex := regexp.MustCompile(`Device:\s*([^\s,)]+)`)

	var services []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if m := ordinalRegex.FindStringSubmatch(line); m != nil {
			service := strings.TrimSpace(m[1])

			device := ""
			if scanner.Scan() {
				next := scanner.Text()
				if dm := deviceRegex.FindStringSubmatch(next); dm != nil {
					device = dm[1]
				}
			}

			if onlyActiveDevice {
				var matchIface *net.Interface
				for _, i := range ifaces {
					if i.Name != device {
						continue
					}
					if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagRunning == 0 {
						continue
					}
					addrs, _ := i.Addrs()
					if len(addrs) == 0 {
						continue
					}
					matchIface = &i
					break
				}
				if matchIface != nil {
					services = append(services, service)
				}
			} else {
				services = append(services, service)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("扫描输出时出错：%w", err)
	}

	if len(services) == 0 {
		return nil, fmt.Errorf("未找到活跃的网络服务")
	}

	return services, nil
}

func execNetworksetupConcurrent(service string, commands [][]string) error {
	errChan := make(chan error, len(commands))
	var wg sync.WaitGroup

	for _, cmd := range commands {
		wg.Add(1)
		go func(args []string) {
			defer wg.Done()
			if err := exec.Command("networksetup", args...).Run(); err != nil {
				errChan <- fmt.Errorf("执行 networksetup %v 时出错，服务 %s: %w", args, service, err)
			}
		}(append([]string{cmd[0]}, append([]string{service}, cmd[1:]...)...))
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	for err := range errChan {
		if err != nil {
			return err
		}
	}

	return nil
}

func execNetworksetupSerial(service string, commands [][]string) error {
	for _, cmd := range commands {
		args := append([]string{cmd[0]}, append([]string{service}, cmd[1:]...)...)
		if err := exec.Command("networksetup", args...).Run(); err != nil {
			return fmt.Errorf("执行 networksetup %v 时出错，服务 %s: %w", args, service, err)
		}
	}
	return nil
}

func applyNetworkServices(services []string, commands [][]string, concurrent bool) error {
	if !concurrent {
		for _, service := range services {
			if err := execNetworksetupSerial(service, commands); err != nil {
				return err
			}
		}
		return nil
	}

	errChan := make(chan error, len(services))
	var wg sync.WaitGroup

	for _, service := range services {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			if err := execNetworksetupConcurrent(svc, commands); err != nil {
				errChan <- err
			}
		}(service)
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	for err := range errChan {
		if err != nil {
			return err
		}
	}
	return nil
}

func parseProxy(cmd *exec.Cmd) (enabled bool, host, port string) {
	if output, err := cmd.Output(); err == nil {
		for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
			switch {
			case strings.HasPrefix(line, "Enabled: Yes"):
				enabled = true
			case strings.HasPrefix(line, "Server: "):
				host = strings.TrimPrefix(line, "Server: ")
			case strings.HasPrefix(line, "Port: "):
				port = strings.TrimPrefix(line, "Port: ")
			}
		}
	}
	return
}
