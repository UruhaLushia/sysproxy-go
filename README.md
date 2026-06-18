# sysproxy-go

`sysproxy-go` 是一个跨平台系统代理设置库，也提供简单的命令行工具。

支持 Windows、Linux 和 macOS，可设置 HTTP/HTTPS/SOCKS 统一代理、PAC 代理，查询当前代理状态，禁用代理，并监听或守护系统代理设置变更。

## 安装

```bash
go install github.com/UruhaLushia/sysproxy-go@latest
```

`go install` 默认生成的可执行文件名是 `sysproxy-go`。下文使用 `sysproxy` 表示命令名，可按实际二进制名称或重命名后的名称替换。

作为库使用：

```bash
go get github.com/UruhaLushia/sysproxy-go
```

## 命令行用法

设置普通代理：

```bash
sysproxy proxy --server 127.0.0.1:7890 --bypass "localhost,127.0.0.1"
```

设置普通代理，并在设置系统代理前等待代理服务端口可连接：

```bash
sysproxy proxy --server 127.0.0.1:7890 --wait-server
```

设置 PAC 代理：

```bash
sysproxy pac --url http://127.0.0.1:7890/proxy.pac
```

查看当前代理设置：

```bash
sysproxy status
```

禁用代理：

```bash
sysproxy disable
```

监听系统代理变更：

```bash
sysproxy watch
```

守护系统代理设置，检测到变更后自动恢复：

```bash
sysproxy guard --server 127.0.0.1:7890 --bypass "localhost,127.0.0.1"
```

守护 PAC 设置：

```bash
sysproxy guard --url http://127.0.0.1:7890/proxy.pac
```

### 通用选项

```text
-a, --only-active-device   仅对活跃的网络设备生效
-d, --device string        指定网络设备
    --multithread          启用并发设置，macOS 默认开启，Windows 默认关闭
    --registry             Windows 使用注册表设置或查询当前用户代理
```

### 代理选项

`proxy` 和普通代理模式的 `guard` 支持：

```text
    --wait-server   设置系统代理前一直等待代理服务器可用
```

`--wait-server` 会在写入系统代理前轮询 `--server` 对应的 TCP 地址，直到端口可连接或进程被取消。未设置时保持原有行为，不等待。

`--device` 的含义由平台决定：Windows 为连接名称，macOS 为 `networksetup` 中的网络服务名称。Windows 注册表模式不支持指定网络设备。

### 可选 HTTP 服务

HTTP 服务默认不编译，需要显式启用 build tag：

```bash
go build -tags sysproxy_server
```

启动 TCP 服务：

```bash
sysproxy server --network tcp --listen 127.0.0.1:9090
```

启动 Unix Domain Socket 服务：

```bash
sysproxy server --network unix --listen /tmp/sparkle-helper.sock
```

服务提供 `/ping`、`/status`、`/proxy`、`/pac`、`/disable` 和 SSE `/events`。`/events` 会返回 `text/event-stream`，在系统代理变更时推送 `update` 事件。

### 目标用户选项

Windows:

```text
    --user string   指定系统用户
    --sid string    指定用户 SID
    --pid int       指定会话进程 PID
```

Linux:

```text
    --user string       指定系统用户
    --pid int           指定会话进程 PID
    --uid uint32        指定用户 UID
    --gid uint32        指定用户 GID
    --env stringArray   指定会话环境变量 KEY=VALUE，可重复
```

Linux 下 `--user` 会按用户名解析 UID/GID，并尽量从该用户的图形会话进程恢复 `XDG_CURRENT_DESKTOP`、`XDG_RUNTIME_DIR`、`DBUS_SESSION_BUS_ADDRESS` 等环境；也可以直接传 `--pid` 在命令启动时从该进程恢复会话环境，或传 `--uid`、`--gid`、`--env`。

Windows 下 `--user` 会解析为用户 SID；`--sid` 可直接指定 SID，`--pid` 会在命令启动时从进程令牌解析用户 SID。指定 `--user`、`--sid` 或 `--pid` 时会按该用户的 `HKEY_USERS\<sid>` 代理注册表项读写，不需要额外指定 `--registry`。

## Go API

```go
package main

import "github.com/UruhaLushia/sysproxy-go/sysproxy"

func main() {
	err := sysproxy.SetProxy(&sysproxy.Options{
		Proxy:  "127.0.0.1:7890",
		Bypass: "localhost,127.0.0.1",
	})
	if err != nil {
		panic(err)
	}
}
```

常用方法：

```go
sysproxy.SetProxy(opt)
sysproxy.SetPac(opt)
sysproxy.DisableProxy(opt)
sysproxy.QueryProxySettings(opt)
sysproxy.WaitProxySettingsChange(ctx, opt)
```

`Options` 支持：

```go
type Options struct {
	Proxy            string
	Bypass           string
	PACURL           string
	Device           string
	OnlyActiveDevice bool
	UserSID          string
	PeerPID          int
	PeerUID          uint32
	PeerGID          uint32
	Environment      []string
	Concurrent       *bool
	UseRegistry      bool
}
```

可使用 `sysproxy.OptionsForUser(name)` 或 `sysproxy.OptionsForProcess(pid)` 生成目标参数。Linux 下如果代理操作需要使用调用方会话环境，可以传入 `Environment`，或传入 `PeerPID`、`PeerUID`、`PeerGID` 让库从调用方进程恢复 `XDG_CURRENT_DESKTOP`、`XDG_RUNTIME_DIR`、`DBUS_SESSION_BUS_ADDRESS` 等会话变量。Windows 下可传入 `UserSID` 直接指定目标用户注册表。

## 平台行为

### Windows

默认通过 WinINet API 设置代理，并刷新系统代理状态。默认会覆盖当前用户的默认连接和枚举到的拨号/VPN 连接；使用 `--only-active-device` 时只操作默认连接。

可使用 `--device` 指定连接名称。连接名称通常需要使用英文系统接口名称。

`--registry` 会直接读写当前用户的：

```text
HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings
```

注册表模式适合只需要修改当前用户代理配置、且不需要指定连接名称的场景。

指定 `--user`、`--sid` 或 `--pid` 时会直接读写目标用户的：

```text
HKEY_USERS\<sid>\Software\Microsoft\Windows\CurrentVersion\Internet Settings
```

### Linux

Linux 通过桌面环境工具修改代理：

- KDE: `kwriteconfig5` / `kwriteconfig6`
- GNOME、Unity、Cinnamon、niri: `gsettings`

监听代理变更时，GNOME 使用 `gsettings monitor`，KDE 使用 inotify 监听 `kioslaverc`。

### macOS

macOS 通过 `networksetup` 设置代理。默认会对所有网络服务生效；使用 `--only-active-device` 时只对当前活跃并有地址的网络服务生效；使用 `--device` 可指定网络服务名称。

默认并发执行多个 `networksetup` 调用，可通过 `--multithread=false` 改为串行执行。

## 状态输出

`status` 返回 JSON，格式大致如下：

```json
{
  "proxy": {
    "enable": true,
    "same_for_all": true,
    "servers": {
      "http_server": "127.0.0.1:7890",
      "https_server": "127.0.0.1:7890",
      "socks_server": "127.0.0.1:7890"
    },
    "bypass": "localhost,127.0.0.1"
  },
  "pac": {
    "enable": false,
    "url": ""
  }
}
```
