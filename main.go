package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/UruhaLushia/sysproxy-go/sysproxy"

	"github.com/spf13/cobra"
)

var (
	server string
	bypass string
	pacUrl string

	listen           string
	device           string
	onlyActiveDevice bool
	multiThread      bool
	useRegistry      bool
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
		err := sysproxy.SetProxy(&sysproxy.Options{
			Proxy:            server,
			Bypass:           bypass,
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			Concurrent:       sysproxy.Bool(multiThread),
			UseRegistry:      useRegistry,
		})
		if err != nil {
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
		err := sysproxy.SetPac(&sysproxy.Options{
			PACURL:           pacUrl,
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			Concurrent:       sysproxy.Bool(multiThread),
			UseRegistry:      useRegistry,
		})
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
		err := sysproxy.DisableProxy(&sysproxy.Options{
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			Concurrent:       sysproxy.Bool(multiThread),
			UseRegistry:      useRegistry,
		})
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
		status, err := sysproxy.QueryProxySettings(&sysproxy.Options{
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			UseRegistry:      useRegistry,
		})
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

		opts := &sysproxy.Options{
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			UseRegistry:      useRegistry,
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

		opts := &sysproxy.Options{
			Proxy:            server,
			Bypass:           bypass,
			PACURL:           pacUrl,
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			Concurrent:       sysproxy.Bool(multiThread),
			UseRegistry:      useRegistry,
		}

		watchOpts := &sysproxy.Options{
			Device:           device,
			OnlyActiveDevice: onlyActiveDevice,
			UseRegistry:      useRegistry,
		}

		var applyFn func() error
		if pacUrl != "" {
			applyFn = func() error {
				return sysproxy.SetPac(opts)
			}
		} else {
			applyFn = func() error {
				return sysproxy.SetProxy(opts)
			}
		}

		if err := applyFn(); err != nil {
			fmt.Println("初始设置代理失败：", err)
			return
		}
		fmt.Println("代理已设置，开始守护...")

		for {
			if err := sysproxy.WaitProxySettingsChange(ctx, watchOpts); err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				fmt.Println("监听代理设置失败：", err)
				return
			}
			fmt.Println("检测到代理设置变更，正在恢复...")
			if err := applyFn(); err != nil {
				fmt.Println("恢复代理设置失败：", err)
			} else {
				fmt.Println("代理设置已恢复")
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

	proxyCmd.Flags().StringVarP(&server, "server", "s", "", "代理服务器地址")
	proxyCmd.Flags().StringVarP(&bypass, "bypass", "b", "", "绕过地址")

	pacCmd.Flags().StringVarP(&pacUrl, "url", "u", "", "pac 地址")

	guardCmd.Flags().StringVarP(&server, "server", "s", "", "代理服务器地址")
	guardCmd.Flags().StringVarP(&bypass, "bypass", "b", "", "绕过地址")
	guardCmd.Flags().StringVarP(&pacUrl, "url", "u", "", "pac 地址")

	serverCmd.Flags().StringVarP(&listen, "listen", "l", "/tmp/sparkle-helper.sock", "监听地址")
}

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
