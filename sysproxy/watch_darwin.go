//go:build darwin

package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const systemConfigurationPreferencesPath = "/Library/Preferences/SystemConfiguration/preferences.plist"

func WaitProxySettingsChange(ctx context.Context, _ *Options) error {
	if ctx == nil {
		ctx = context.Background()
	}

	fd, err := unix.Open(systemConfigurationPreferencesPath, unix.O_EVTONLY, 0)
	if err != nil {
		return fmt.Errorf("打开 macOS 系统代理配置监听文件失败：%w", err)
	}
	defer unix.Close(fd)

	kq, err := unix.Kqueue()
	if err != nil {
		return fmt.Errorf("初始化 macOS 系统代理配置监听失败：%w", err)
	}
	defer unix.Close(kq)

	changes := make([]unix.Kevent_t, 1)
	unix.SetKevent(&changes[0], fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_ENABLE|unix.EV_CLEAR)
	changes[0].Fflags = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB |
		unix.NOTE_RENAME | unix.NOTE_DELETE | unix.NOTE_REVOKE

	if _, err := unix.Kevent(kq, changes, nil, nil); err != nil {
		return fmt.Errorf("注册 macOS 系统代理配置监听失败：%w", err)
	}

	events := make([]unix.Kevent_t, 1)
	timeout := &unix.Timespec{Sec: 1}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		n, err := unix.Kevent(kq, nil, events, timeout)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("等待 macOS 系统代理配置变更失败：%w", err)
		}
		if n > 0 {
			return nil
		}

		if _, err := os.Stat(systemConfigurationPreferencesPath); err != nil {
			return fmt.Errorf("读取 macOS 系统代理配置文件失败：%w", err)
		}
	}
}
