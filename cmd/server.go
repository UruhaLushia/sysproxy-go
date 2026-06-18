//go:build sysproxy_server

package cmd

import (
	"fmt"

	sysproxyserver "github.com/UruhaLushia/sysproxy-go/server"

	"github.com/spf13/cobra"
)

var (
	listenNetwork string
	listenAddress string
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动监听服务",
	Run: func(cmd *cobra.Command, args []string) {
		err := sysproxyserver.Start(sysproxyserver.Options{
			Network: listenNetwork,
			Address: listenAddress,
		})
		if err != nil {
			fmt.Println("启动代理服务失败：", err)
			return
		}
		fmt.Println("代理服务已启动")
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVarP(&listenNetwork, "network", "n", sysproxyserver.DefaultNetwork, "监听网络：tcp、tcp4、tcp6、unix")
	serverCmd.Flags().StringVarP(&listenAddress, "listen", "l", "", "监听地址；tcp 默认 127.0.0.1:0，unix 默认 /tmp/sparkle-helper.sock")
}
