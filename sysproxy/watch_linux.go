//go:build linux

package sysproxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

func WaitProxySettingsChange(ctx context.Context, opt *Options) error {
	return WaitProxySettingsChangeReady(ctx, opt, nil)
}

func WaitProxySettingsChangeReady(ctx context.Context, opt *Options, ready func()) error {
	if ctx == nil {
		ctx = context.Background()
	}

	e := &Environment{}
	if err := e.Init(opt); err != nil {
		return err
	}

	switch {
	case e.isGnome:
		return waitGnomeProxySettingsChange(ctx, e, ready)
	case e.isKde:
		return waitKDEProxySettingsChange(ctx, e, ready)
	default:
		return fmt.Errorf("不支持的桌面：%s", e.desktop)
	}
}

func waitGnomeProxySettingsChange(ctx context.Context, e *Environment, ready func()) error {
	monitorCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	schemas := []string{
		"org.gnome.system.proxy",
		"org.gnome.system.proxy.http",
		"org.gnome.system.proxy.https",
		"org.gnome.system.proxy.ftp",
		"org.gnome.system.proxy.socks",
	}

	changed := make(chan struct{}, 1)
	errCh := make(chan error, len(schemas))
	var wg sync.WaitGroup

	for _, schema := range schemas {
		wg.Add(1)
		go func(schema string) {
			defer wg.Done()
			if err := waitGsettingsSchemaChange(monitorCtx, e, schema, changed); err != nil {
				select {
				case errCh <- err:
				case <-monitorCtx.Done():
				}
			}
		}(schema)
	}

	go func() {
		wg.Wait()
		close(errCh)
	}()

	if ready != nil {
		ready()
	}

	var firstErr error
	for {
		select {
		case <-ctx.Done():
			cancel()
			return ctx.Err()
		case <-changed:
			cancel()
			return nil
		case err, ok := <-errCh:
			if !ok {
				if firstErr != nil {
					return firstErr
				}
				return fmt.Errorf("GNOME 代理设置监听已退出")
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
}

func waitGsettingsSchemaChange(ctx context.Context, e *Environment, schema string, changed chan<- struct{}) error {
	cmd := execAsCurrentUserContext(ctx, e.ctx, "gsettings", "monitor", schema)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("监听 GNOME 代理设置失败：%s: %w", schema, err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 GNOME 代理设置监听失败：%s: %w", schema, err)
	}

	scanner := bufio.NewScanner(stdout)
	if scanner.Scan() {
		select {
		case changed <- struct{}{}:
		case <-ctx.Done():
		}
	}

	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if scanErr != nil {
		return fmt.Errorf("读取 GNOME 代理设置监听失败：%s: %w", schema, scanErr)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf("GNOME 代理设置监听退出：%s: %w: %s", schema, waitErr, message)
		}
		return fmt.Errorf("GNOME 代理设置监听退出：%s: %w", schema, waitErr)
	}
	return fmt.Errorf("GNOME 代理设置监听已退出：%s", schema)
}

func waitKDEProxySettingsChange(ctx context.Context, e *Environment, ready func()) error {
	configPath, err := kdeProxyConfigPath(e)
	if err != nil {
		return err
	}

	watchPath := configPath
	watchDir := false
	if _, err := os.Stat(configPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("读取 KDE 代理配置文件失败：%w", err)
		}
		watchPath = filepath.Dir(configPath)
		watchDir = true
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("初始化 KDE 代理设置监听失败：%w", err)
	}
	defer unix.Close(fd)

	mask := uint32(unix.IN_CLOSE_WRITE | unix.IN_MODIFY | unix.IN_MOVED_TO | unix.IN_CREATE | unix.IN_ATTRIB | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF)
	if _, err := unix.InotifyAddWatch(fd, watchPath, mask); err != nil {
		return fmt.Errorf("监听 KDE 代理配置文件失败：%s: %w", watchPath, err)
	}

	if ready != nil {
		ready()
	}

	buffer := make([]byte, unix.SizeofInotifyEvent+unix.NAME_MAX+1)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		pollFDs := []unix.PollFd{{
			Fd:     int32(fd),
			Events: unix.POLLIN,
		}}
		n, err := unix.Poll(pollFDs, 1000)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("等待 KDE 代理配置文件变更失败：%w", err)
		}
		if n == 0 {
			continue
		}

		readLen, err := unix.Read(fd, buffer)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("读取 KDE 代理配置文件变更失败：%w", err)
		}
		if readLen <= 0 {
			continue
		}
		if !watchDir || inotifyEventsContainName(buffer[:readLen], filepath.Base(configPath)) {
			return nil
		}
	}
}

func kdeProxyConfigPath(e *Environment) (string, error) {
	configHome := e.ctx.envMap["XDG_CONFIG_HOME"]
	if configHome == "" {
		home := e.ctx.envMap["HOME"]
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("无法获取用户配置目录：%w", err)
			}
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "kioslaverc"), nil
}

func inotifyEventsContainName(data []byte, target string) bool {
	for offset := 0; offset+unix.SizeofInotifyEvent <= len(data); {
		event := (*unix.InotifyEvent)(unsafe.Pointer(&data[offset]))
		offset += unix.SizeofInotifyEvent

		nameLen := int(event.Len)
		if offset+nameLen > len(data) {
			return false
		}
		name := strings.TrimRight(string(data[offset:offset+nameLen]), "\x00")
		offset += nameLen

		if name == target {
			return true
		}
	}
	return false
}
