package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/UruhaLushia/sysproxy-go/sysproxy"

	"github.com/spf13/cobra"
)

var (
	server     string
	bypass     string
	pacUrl     string
	waitServer bool

	listen           string
	device           string
	onlyActiveDevice bool
	multiThread      bool
	useRegistry      bool
	targetUser       string
	targetSID        string
	peerPID          int
	peerUID          uint32
	peerGID          uint32
	peerEnv          []string
)

const (
	waitServerDialTimeout = 500 * time.Millisecond
	waitServerInitialPoll = time.Second
	waitServerMaxPoll     = 30 * time.Second
)

var cmd = &cobra.Command{
	Use:   "sysproxy",
	Short: "系统代理设置工具",
}

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "设置系统代理",
	Run: func(cmd *cobra.Command, args []string) {
		t := time.Now()
		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}
		opts.Proxy = server
		opts.Bypass = bypass

		ctx := context.Background()
		if waitServer {
			var stop context.CancelFunc
			ctx, stop = signal.NotifyContext(ctx, os.Interrupt)
			defer stop()
		}

		err = setProxy(ctx, opts)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			fmt.Println("设置代理失败：", err)
			return
		}
		fmt.Println("代理设置成功，耗时：", time.Since(t))
	},
}

var pacCmd = &cobra.Command{
	Use:   "pac",
	Short: "设置 PAC 代理",
	Run: func(cmd *cobra.Command, args []string) {
		t := time.Now()
		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}
		opts.PACURL = pacUrl
		err = sysproxy.SetPac(opts)
		if err != nil {
			fmt.Println("设置 PAC 代理失败：", err)
			return
		}
		fmt.Println("PAC 代理设置成功，耗时：", time.Since(t))
	},
}

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "取消代理设置",
	Run: func(cmd *cobra.Command, args []string) {
		t := time.Now()
		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}
		err = sysproxy.DisableProxy(opts)
		if err != nil {
			fmt.Println("取消代理设置失败：", err)
			return
		}
		fmt.Println("代理设置已取消，耗时：", time.Since(t))
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "查看当前代理设置",
	Run: func(cmd *cobra.Command, args []string) {
		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}
		status, err := sysproxy.QueryProxySettings(opts)
		if err != nil {
			fmt.Println("查询代理设置失败：", err)
			return
		}
		statusJSON, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			fmt.Println("格式化 JSON 失败：", err)
			return
		}
		fmt.Println(string(statusJSON))
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "监听系统代理设置变更",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}

		for {
			if err := sysproxy.WaitProxySettingsChange(ctx, opts); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				fmt.Println("监听代理设置失败：", err)
				return
			}
			fmt.Println("update")
		}
	},
}

var guardCmd = &cobra.Command{
	Use:   "guard",
	Short: "守护系统代理设置，检测到变更后自动恢复",
	Run: func(cmd *cobra.Command, args []string) {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		opts, err := commandOptions()
		if err != nil {
			fmt.Println("解析命令参数失败：", err)
			return
		}
		opts.Proxy = server
		opts.Bypass = bypass
		opts.PACURL = pacUrl
		watchOpts := cloneOptions(opts)

		var mode guardMode
		if pacUrl != "" {
			mode = guardModePAC
		} else {
			mode = guardModeProxy
		}

		if err := applyGuardSettings(ctx, mode, opts); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			fmt.Println("初始设置代理失败：", err)
			return
		}
		expected, err := queryGuardSnapshot(mode, opts)
		if err != nil {
			fmt.Println("读取守护目标失败：", err)
			return
		}
		fillGuardApplyOptions(mode, opts, expected)
		fmt.Println("代理已设置，开始守护...")

		for {
			watchCtx, cancelWatch, watchReady, watchErr := startGuardWatch(ctx, watchOpts)
			select {
			case <-ctx.Done():
				cancelWatch()
				return
			case <-watchReady:
			}

			current, err := queryGuardSnapshot(mode, opts)
			if err != nil {
				fmt.Println("检查代理设置失败：", err)
				if !waitGuardNextChange(ctx, cancelWatch, watchErr) {
					return
				}
				continue
			}

			if !reflect.DeepEqual(expected, current) {
				fmt.Println("检测到代理设置变更，正在恢复...")
				if err := applyGuardSettings(ctx, mode, opts); err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					fmt.Println("恢复代理设置失败：", err)
					if !waitGuardNextChange(ctx, nil, watchErr) {
						return
					}
					continue
				}
				current, err = queryGuardSnapshot(mode, opts)
				if err != nil {
					fmt.Println("恢复后检查代理设置失败：", err)
					if !waitGuardNextChange(ctx, nil, watchErr) {
						return
					}
					continue
				}
				if !reflect.DeepEqual(expected, current) {
					fmt.Printf("恢复后代理设置仍不匹配：expected=%+v current=%+v\n", expected, current)
					if !waitGuardNextChange(ctx, nil, watchErr) {
						return
					}
					continue
				}
				fmt.Println("代理设置已恢复")
			}

			select {
			case <-ctx.Done():
				cancelWatch()
				return
			case err := <-watchErr:
				if err == nil {
					continue
				}
				if errors.Is(err, context.Canceled) {
					return
				}
				fmt.Println("监听代理设置失败：", err)
				return
			case <-watchCtx.Done():
				if ctx.Err() != nil {
					return
				}
			}
		}
	},
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动监听服务",
	Run: func(cmd *cobra.Command, args []string) {
		err := sysproxy.Start(listen)
		if err != nil {
			fmt.Println("启动代理服务失败：", err)
			return
		}
		fmt.Println("代理服务已启动")
	},
}

type guardMode string

const (
	guardModeProxy guardMode = "proxy"
	guardModePAC   guardMode = "pac"
)

type guardSnapshot struct {
	Mode             guardMode
	ProxyEnable      bool
	ProxySameForAll  bool
	ProxyServers     map[string]string
	ProxyBypass      string
	ProxyPACConflict bool
	PACEnable        bool
	PACURL           string
	PACProxyConflict bool
}

func startGuardWatch(ctx context.Context, opts *sysproxy.Options) (context.Context, context.CancelFunc, <-chan struct{}, <-chan error) {
	watchCtx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	errCh := make(chan error, 1)
	var readyOnce sync.Once

	go func() {
		err := sysproxy.WaitProxySettingsChangeReady(watchCtx, opts, func() {
			readyOnce.Do(func() { close(ready) })
		})
		readyOnce.Do(func() { close(ready) })
		errCh <- err
	}()

	return watchCtx, cancel, ready, errCh
}

func waitGuardNextChange(ctx context.Context, cancelWatch context.CancelFunc, watchErr <-chan error) bool {
	if cancelWatch != nil {
		defer cancelWatch()
	}
	select {
	case <-ctx.Done():
		return false
	case <-watchErr:
		return true
	}
}

func applyGuardSettings(ctx context.Context, mode guardMode, opts *sysproxy.Options) error {
	switch mode {
	case guardModeProxy:
		return setProxy(ctx, opts)
	case guardModePAC:
		return sysproxy.SetPac(opts)
	default:
		return fmt.Errorf("未知守护模式：%s", mode)
	}
}

func setProxy(ctx context.Context, opts *sysproxy.Options) error {
	if waitServer {
		if err := waitProxyServerAvailable(ctx, opts.Proxy); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return sysproxy.SetProxy(opts)
}

func waitProxyServerAvailable(ctx context.Context, proxy string) error {
	addr, err := proxyTCPAddress(proxy)
	if err != nil {
		return err
	}
	fmt.Printf("等待代理服务器可用：%s\n", addr)

	dialer := net.Dialer{Timeout: waitServerDialTimeout}
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	nextPoll := time.Duration(0)
	for {
		if nextPoll > 0 {
			timer.Reset(nextPoll)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
			}
		}

		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if nextPoll == 0 {
			nextPoll = waitServerInitialPoll
		} else {
			nextPoll *= 2
			if nextPoll > waitServerMaxPoll {
				nextPoll = waitServerMaxPoll
			}
		}
	}
}

func proxyTCPAddress(proxy string) (string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return "", fmt.Errorf("--wait-server 需要同时指定 --server")
	}
	if _, _, err := net.SplitHostPort(proxy); err == nil {
		return proxy, nil
	}
	return "", fmt.Errorf("invalid proxy address: %s", proxy)
}

func queryGuardSnapshot(mode guardMode, opts *sysproxy.Options) (guardSnapshot, error) {
	config, err := sysproxy.QueryProxySettings(guardQueryOptions(opts))
	if err != nil {
		return guardSnapshot{}, err
	}
	return newGuardSnapshot(mode, config), nil
}

func guardQueryOptions(opts *sysproxy.Options) *sysproxy.Options {
	queryOpts := cloneOptions(opts)
	if runtime.GOOS == "windows" && queryOpts.Device == "" {
		queryOpts.UseRegistry = true
	}
	return queryOpts
}

func newGuardSnapshot(mode guardMode, config *sysproxy.ProxyConfig) guardSnapshot {
	snapshot := guardSnapshot{Mode: mode}
	if config == nil {
		return snapshot
	}

	switch mode {
	case guardModeProxy:
		snapshot.ProxyEnable = config.Proxy.Enable
		snapshot.ProxySameForAll = config.Proxy.SameForAll
		snapshot.ProxyServers = copyStringMap(config.Proxy.Servers)
		snapshot.ProxyBypass = config.Proxy.Bypass
		snapshot.ProxyPACConflict = config.PAC.Enable
	case guardModePAC:
		snapshot.PACEnable = config.PAC.Enable
		snapshot.PACURL = config.PAC.URL
		snapshot.PACProxyConflict = config.Proxy.Enable
	}

	return snapshot
}

func fillGuardApplyOptions(mode guardMode, opts *sysproxy.Options, expected guardSnapshot) {
	switch mode {
	case guardModeProxy:
		opts.Proxy = firstNonEmpty(
			expected.ProxyServers["http_server"],
			expected.ProxyServers["https_server"],
			expected.ProxyServers["socks_server"],
			opts.Proxy,
		)
		opts.Bypass = expected.ProxyBypass
	case guardModePAC:
		opts.PACURL = expected.PACURL
	}
}

func commandOptions() (*sysproxy.Options, error) {
	opts := &sysproxy.Options{
		Device:           device,
		OnlyActiveDevice: onlyActiveDevice,
		Concurrent:       sysproxy.Bool(multiThread),
		UseRegistry:      useRegistry,
	}
	if err := applyTargetOptions(opts); err != nil {
		return nil, err
	}
	return opts, nil
}

func applyTargetOptions(opts *sysproxy.Options) error {
	switch runtime.GOOS {
	case "windows":
		return applyWindowsTargetOptions(opts)
	case "linux":
		return applyLinuxTargetOptions(opts)
	default:
		return nil
	}
}

func applyWindowsTargetOptions(opts *sysproxy.Options) error {
	if targetUser != "" {
		userOpts, err := sysproxy.OptionsForUser(targetUser)
		if err != nil {
			return err
		}
		mergeTargetOptions(opts, userOpts)
	}
	if peerPID > 0 {
		processOpts, err := sysproxy.OptionsForProcess(peerPID)
		if err != nil {
			return err
		}
		mergeTargetOptions(opts, processOpts)
	}
	if targetSID != "" {
		opts.UserSID = targetSID
	}
	return nil
}

func applyLinuxTargetOptions(opts *sysproxy.Options) error {
	if targetUser != "" {
		userOpts, err := sysproxy.OptionsForUser(targetUser)
		if err != nil {
			return err
		}
		mergeTargetOptions(opts, userOpts)
	}
	if peerPID > 0 {
		processOpts, err := sysproxy.OptionsForProcess(peerPID)
		if err != nil {
			return err
		}
		mergeTargetOptions(opts, processOpts)
	}
	if peerUID != 0 {
		opts.PeerUID = peerUID
	}
	if peerGID != 0 {
		opts.PeerGID = peerGID
	}
	if len(peerEnv) > 0 {
		for _, item := range peerEnv {
			if !strings.Contains(item, "=") {
				return fmt.Errorf("--env 需要 KEY=VALUE：%s", item)
			}
		}
		opts.Environment = append([]string(nil), peerEnv...)
	}
	return nil
}

func mergeTargetOptions(dst, src *sysproxy.Options) {
	if dst == nil || src == nil {
		return
	}
	if src.UserSID != "" {
		dst.UserSID = src.UserSID
	}
	if src.PeerPID != 0 {
		dst.PeerPID = src.PeerPID
	}
	if src.PeerUID != 0 {
		dst.PeerUID = src.PeerUID
	}
	if src.PeerGID != 0 {
		dst.PeerGID = src.PeerGID
	}
	if len(src.Environment) > 0 {
		dst.Environment = append([]string(nil), src.Environment...)
	}
}

func cloneOptions(opt *sysproxy.Options) *sysproxy.Options {
	if opt == nil {
		return &sysproxy.Options{}
	}
	copied := *opt
	return &copied
}

func copyStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		if value == "" {
			continue
		}
		dst[key] = value
	}
	if len(dst) == 0 {
		return nil
	}
	return dst
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func init() {
	cmd.AddCommand(proxyCmd)
	cmd.AddCommand(pacCmd)
	cmd.AddCommand(disableCmd)
	cmd.AddCommand(statusCmd)
	cmd.AddCommand(watchCmd)
	cmd.AddCommand(guardCmd)
	cmd.AddCommand(serverCmd)

	cmd.PersistentFlags().BoolVarP(&onlyActiveDevice, "only-active-device", "a", false, "仅对活跃的网络设备生效")
	cmd.PersistentFlags().StringVarP(&device, "device", "d", "", "指定网络设备")
	cmd.PersistentFlags().BoolVar(&multiThread, "multithread", sysproxy.DefaultConcurrent(), "启用多线程并发设置；macOS 默认开启，Windows 默认关闭")
	cmd.PersistentFlags().BoolVar(&useRegistry, "registry", false, "Windows 使用注册表设置/查询代理，不调用 win32 API")
	registerTargetFlags()

	proxyCmd.Flags().StringVarP(&server, "server", "s", "", "代理服务器地址")
	proxyCmd.Flags().StringVarP(&bypass, "bypass", "b", "", "绕过地址")
	proxyCmd.Flags().BoolVar(&waitServer, "wait-server", false, "设置系统代理前一直等待代理服务器可用")

	pacCmd.Flags().StringVarP(&pacUrl, "url", "u", "", "pac 地址")

	guardCmd.Flags().StringVarP(&server, "server", "s", "", "代理服务器地址")
	guardCmd.Flags().StringVarP(&bypass, "bypass", "b", "", "绕过地址")
	guardCmd.Flags().StringVarP(&pacUrl, "url", "u", "", "pac 地址")
	guardCmd.Flags().BoolVar(&waitServer, "wait-server", false, "设置系统代理前一直等待代理服务器可用")

	serverCmd.Flags().StringVarP(&listen, "listen", "l", "/tmp/sparkle-helper.sock", "监听地址")
}

func registerTargetFlags() {
	switch runtime.GOOS {
	case "windows":
		cmd.PersistentFlags().StringVar(&targetUser, "user", "", "指定系统用户")
		cmd.PersistentFlags().StringVar(&targetSID, "sid", "", "Windows 指定用户 SID")
		cmd.PersistentFlags().IntVar(&peerPID, "pid", 0, "指定会话进程 PID")
	case "linux":
		cmd.PersistentFlags().StringVar(&targetUser, "user", "", "指定系统用户")
		cmd.PersistentFlags().IntVar(&peerPID, "pid", 0, "指定会话进程 PID")
		cmd.PersistentFlags().Uint32Var(&peerUID, "uid", 0, "Linux 指定用户 UID")
		cmd.PersistentFlags().Uint32Var(&peerGID, "gid", 0, "Linux 指定用户 GID")
		cmd.PersistentFlags().StringArrayVar(&peerEnv, "env", nil, "Linux 指定会话环境变量 KEY=VALUE，可重复")
	}
}

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
