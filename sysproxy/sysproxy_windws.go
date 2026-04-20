//go:build windows

package sysproxy

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	INTERNET_OPTION_REFRESH                = 37
	INTERNET_OPTION_PROXY_SETTINGS_CHANGED = 39
	INTERNET_OPTION_PER_CONNECTION_OPTION  = 75

	INTERNET_PER_CONN_FLAGS          = 1
	INTERNET_PER_CONN_PROXY_SERVER   = 2
	INTERNET_PER_CONN_PROXY_BYPASS   = 3
	INTERNET_PER_CONN_AUTOCONFIG_URL = 4

	PROXY_TYPE_DIRECT         = 1
	PROXY_TYPE_PROXY          = 2
	PROXY_TYPE_AUTO_PROXY_URL = 4

	internetSettingsRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`
)

var (
	wininet                  = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW   = wininet.NewProc("InternetSetOptionW")
	procInternetQueryOptionW = wininet.NewProc("InternetQueryOptionW")
)

type (
	InternetPerConnOption struct {
		dwOption uint32
		dwValue  uintptr
	}

	InternetPerConnOptionList struct {
		dwSize        uint32
		pszConnection *uint16
		dwOptionCount uint32
		dwOptionError uint32
		pOptions      *InternetPerConnOption
	}
)

func refreshAndApplySettings(options []InternetPerConnOption, opt *Options) error {
	connectionNames, err := getTargetConnections(opt)
	if err != nil {
		return err
	}

	applyConn := func(name string) error {
		var pszConn *uint16
		if name != "" {
			ptr, err := syscall.UTF16PtrFromString(name)
			if err != nil {
				return err
			}
			pszConn = ptr
		}

		localOptions := make([]InternetPerConnOption, len(options))
		copy(localOptions, options)

		list := InternetPerConnOptionList{
			dwSize:        uint32(unsafe.Sizeof(InternetPerConnOptionList{})),
			pszConnection: pszConn,
			dwOptionCount: uint32(len(localOptions)),
			pOptions:      &localOptions[0],
		}

		if ret, _, err := procInternetSetOptionW.Call(
			0,
			INTERNET_OPTION_PER_CONNECTION_OPTION,
			uintptr(unsafe.Pointer(&list)),
			unsafe.Sizeof(list)); ret == 0 {
			return fmt.Errorf("设置 %s 连接失败：%v", name, err)
		}
		return nil
	}

	if resolveConcurrentApply(opt) {
		if err := applyConcurrent(connectionNames, applyConn); err != nil {
			return err
		}
		procInternetSetOptionW.Call(0, INTERNET_OPTION_PROXY_SETTINGS_CHANGED, 0, 0)
		procInternetSetOptionW.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
		return nil
	}

	for _, name := range connectionNames {
		if err := applyConn(name); err != nil {
			return err
		}
	}

	procInternetSetOptionW.Call(0, INTERNET_OPTION_PROXY_SETTINGS_CHANGED, 0, 0)
	procInternetSetOptionW.Call(0, INTERNET_OPTION_REFRESH, 0, 0)
	return nil
}

func applyConcurrent(connectionNames []string, apply func(string) error) error {
	if len(connectionNames) == 0 {
		return nil
	}

	workerCount := min(len(connectionNames), 16)
	if workerCount <= 1 {
		for _, name := range connectionNames {
			if err := apply(name); err != nil {
				return err
			}
		}
		return nil
	}

	jobs := make(chan string, len(connectionNames))
	errCh := make(chan error, len(connectionNames))
	var wg sync.WaitGroup

	for range workerCount {
		wg.Go(func() {
			for name := range jobs {
				if err := apply(name); err != nil {
					errCh <- err
				}
			}
		})
	}

	for _, name := range connectionNames {
		jobs <- name
	}
	close(jobs)

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return err
		}
	}

	return nil
}

func getTargetConnections(opt *Options) ([]string, error) {
	if opt != nil && opt.Device != "" {
		return []string{opt.Device}, nil
	}
	if opt != nil && opt.OnlyActiveDevice {
		return []string{""}, nil
	}

	connectionNames, err := enumAllConnectionNames()
	if err != nil {
		return nil, fmt.Errorf("获取连接名失败：%v", err)
	}

	connectionNames = append(connectionNames, "")
	return connectionNames, nil
}

func DisableProxy(opt *Options) error {
	if useRegistrySettings(opt) {
		return disableProxyRegistry(opt)
	}

	return refreshAndApplySettings([]InternetPerConnOption{{
		dwOption: INTERNET_PER_CONN_FLAGS,
		dwValue:  PROXY_TYPE_DIRECT,
	}}, opt)
}

func SetProxy(opt *Options) error {
	if useRegistrySettings(opt) {
		return setProxyRegistry(opt)
	}

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
	proxyPtr, err := syscall.UTF16PtrFromString(proxy)
	if err != nil {
		return err
	}
	bypassPtr, err := syscall.UTF16PtrFromString(bypass)
	if err != nil {
		return err
	}

	return refreshAndApplySettings([]InternetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: PROXY_TYPE_PROXY},
		{dwOption: INTERNET_PER_CONN_PROXY_SERVER, dwValue: uintptr(unsafe.Pointer(proxyPtr))},
		{dwOption: INTERNET_PER_CONN_PROXY_BYPASS, dwValue: uintptr(unsafe.Pointer(bypassPtr))},
	}, opt)
}

func SetPac(opt *Options) error {
	if useRegistrySettings(opt) {
		return setPacRegistry(opt)
	}

	pacUrl := ""
	if opt != nil {
		pacUrl = opt.PACURL
	}
	if pacUrl == "" {
		return refreshAndApplySettings([]InternetPerConnOption{
			{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: PROXY_TYPE_AUTO_PROXY_URL},
		}, opt)
	}
	pacPtr, err := syscall.UTF16PtrFromString(pacUrl)
	if err != nil {
		return err
	}

	return refreshAndApplySettings([]InternetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS, dwValue: PROXY_TYPE_AUTO_PROXY_URL},
		{dwOption: INTERNET_PER_CONN_AUTOCONFIG_URL, dwValue: uintptr(unsafe.Pointer(pacPtr))},
	}, opt)
}

func QueryProxySettings(opt *Options) (*ProxyConfig, error) {
	if useRegistrySettings(opt) {
		if err := validateRegistryTarget(opt); err != nil {
			return nil, err
		}
		return queryProxySettingsRegistry()
	}

	options := [4]InternetPerConnOption{
		{dwOption: INTERNET_PER_CONN_FLAGS},
		{dwOption: INTERNET_PER_CONN_PROXY_SERVER},
		{dwOption: INTERNET_PER_CONN_PROXY_BYPASS},
		{dwOption: INTERNET_PER_CONN_AUTOCONFIG_URL},
	}

	list := InternetPerConnOptionList{
		dwSize:        uint32(unsafe.Sizeof(InternetPerConnOptionList{})),
		dwOptionCount: 4,
		pOptions:      &options[0],
	}

	if ret, _, err := procInternetQueryOptionW.Call(
		0,
		INTERNET_OPTION_PER_CONNECTION_OPTION,
		uintptr(unsafe.Pointer(&list)),
		uintptr(unsafe.Pointer(&list.dwSize))); ret == 0 {
		return nil, fmt.Errorf("查询失败：%v", err)
	}

	flags := uint32(options[0].dwValue)
	config := &ProxyConfig{}

	config.Proxy.Enable = (flags & PROXY_TYPE_PROXY) != 0
	config.Proxy.Servers = map[string]string{
		"http_server": getString(options[1].dwValue),
	}
	config.Proxy.Bypass = getString(options[2].dwValue)
	config.PAC.Enable = (flags & PROXY_TYPE_AUTO_PROXY_URL) != 0
	config.PAC.URL = getString(options[3].dwValue)

	return config, nil
}

func useRegistrySettings(opt *Options) bool {
	return opt != nil && opt.UseRegistry
}

func validateRegistryTarget(opt *Options) error {
	if opt != nil && opt.Device != "" {
		return fmt.Errorf("注册表模式不支持指定网络设备")
	}
	return nil
}

func disableProxyRegistry(opt *Options) error {
	if err := validateRegistryTarget(opt); err != nil {
		return err
	}

	key, err := openCurrentUserKey(internetSettingsRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	if err := key.SetDWordValue("AutoDetect", 0); err != nil {
		return err
	}
	return deleteRegistryValue(key, "AutoConfigURL")
}

func setProxyRegistry(opt *Options) error {
	if err := validateRegistryTarget(opt); err != nil {
		return err
	}

	proxy := ""
	bypass := ""
	if opt != nil {
		proxy = opt.Proxy
		bypass = opt.Bypass
	}
	if proxy == "" || bypass == "" {
		config, err := queryProxySettingsRegistry()
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

	key, err := openCurrentUserKey(internetSettingsRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := key.SetStringValue("ProxyServer", proxy); err != nil {
		return err
	}
	if err := key.SetStringValue("ProxyOverride", bypass); err != nil {
		return err
	}
	if err := key.SetDWordValue("AutoDetect", 0); err != nil {
		return err
	}
	return deleteRegistryValue(key, "AutoConfigURL")
}

func setPacRegistry(opt *Options) error {
	if err := validateRegistryTarget(opt); err != nil {
		return err
	}

	pacUrl := ""
	if opt != nil {
		pacUrl = opt.PACURL
	}
	if pacUrl == "" {
		config, err := queryProxySettingsRegistry()
		if err != nil {
			return err
		}
		pacUrl = config.PAC.URL
	}

	key, err := openCurrentUserKey(internetSettingsRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()

	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	if err := key.SetDWordValue("AutoDetect", 0); err != nil {
		return err
	}
	if pacUrl == "" {
		return nil
	}
	return key.SetStringValue("AutoConfigURL", pacUrl)
}

func queryProxySettingsRegistry() (*ProxyConfig, error) {
	key, err := openCurrentUserKey(internetSettingsRegistryPath, registry.READ)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	proxyEnable, err := readRegistryDWORD(key, "ProxyEnable")
	if err != nil {
		return nil, err
	}
	proxyServer, err := readRegistryString(key, "ProxyServer")
	if err != nil {
		return nil, err
	}
	proxyOverride, err := readRegistryString(key, "ProxyOverride")
	if err != nil {
		return nil, err
	}
	autoConfigURL, err := readRegistryString(key, "AutoConfigURL")
	if err != nil {
		return nil, err
	}

	config := &ProxyConfig{}
	config.Proxy.Enable = proxyEnable != 0
	config.Proxy.Servers = map[string]string{
		"http_server": proxyServer,
	}
	config.Proxy.Bypass = proxyOverride
	config.PAC.Enable = autoConfigURL != ""
	config.PAC.URL = autoConfigURL

	return config, nil
}

func readRegistryDWORD(key registry.Key, name string) (uint64, error) {
	value, _, err := key.GetIntegerValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, nil
	}
	return value, err
}

func readRegistryString(key registry.Key, name string) (string, error) {
	value, _, err := key.GetStringValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return value, err
}

func deleteRegistryValue(key registry.Key, name string) error {
	err := key.DeleteValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	return err
}

func getString(val uintptr) string {
	if val == 0 {
		return ""
	}
	return syscall.UTF16ToString(*(*[]uint16)(unsafe.Pointer(&struct {
		addr uintptr
		len  int
		cap  int
	}{val, 1024, 1024})))
}

func enumAllConnectionNames() ([]string, error) {
	key, err := openCurrentUserKey(`Software\Microsoft\Windows\CurrentVersion\Internet Settings\Connections`, registry.READ)
	if err != nil {
		return nil, err
	}
	defer key.Close()

	names, err := key.ReadValueNames(0)
	if err != nil {
		return nil, err
	}

	reserved := map[string]struct{}{
		"DefaultConnectionSettings": {},
		"SavedLegacySettings":       {},
	}

	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, ok := reserved[name]; ok {
			continue
		}
		filtered = append(filtered, name)
	}

	return filtered, nil
}
