//go:build windows

package sysproxy

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsConnectionsRegistryPath = internetSettingsRegistryPath + `\Connections`

// WaitProxySettingsChange blocks until Windows reports that current-user proxy
// registry settings changed or the context is canceled.
func WaitProxySettingsChange(ctx context.Context, opt *Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateProxySettingsWatchTarget(opt); err != nil {
		return err
	}

	keys, err := openProxySettingsWatchKeys()
	if err != nil {
		return err
	}
	defer func() {
		for _, key := range keys {
			key.Close()
		}
	}()

	handles := make([]windows.Handle, 0, len(keys)+1)
	for _, key := range keys {
		event, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			closeHandles(handles)
			return fmt.Errorf("创建代理设置变更事件失败：%w", err)
		}
		handles = append(handles, event)

		if err := notifyRegistryKeyChange(key, event); err != nil {
			closeHandles(handles)
			return err
		}
	}

	cancelEvent, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		closeHandles(handles)
		return fmt.Errorf("创建代理设置守护取消事件失败：%w", err)
	}
	handles = append(handles, cancelEvent)
	defer closeHandles(handles)

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = windows.SetEvent(cancelEvent)
		case <-done:
		}
	}()

	index, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("等待代理设置变更失败：%w", err)
	}

	if index == windows.WAIT_OBJECT_0+uint32(len(handles)-1) {
		return ctx.Err()
	}
	return nil
}

func validateProxySettingsWatchTarget(opt *Options) error {
	if useRegistrySettings(opt) {
		return validateRegistryTarget(opt)
	}
	return nil
}

func openProxySettingsWatchKeys() ([]registry.Key, error) {
	paths := []string{
		internetSettingsRegistryPath,
		internetSettingsConnectionsRegistryPath,
	}

	keys := make([]registry.Key, 0, len(paths))
	for _, path := range paths {
		key, err := openCurrentUserKey(path, windows.KEY_NOTIFY)
		if err != nil {
			for _, opened := range keys {
				opened.Close()
			}
			return nil, fmt.Errorf("打开代理设置监听注册表失败：%s: %w", path, err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}

func notifyRegistryKeyChange(key registry.Key, event windows.Handle) error {
	filter := uint32(windows.REG_NOTIFY_CHANGE_LAST_SET | windows.REG_NOTIFY_CHANGE_NAME | windows.REG_NOTIFY_THREAD_AGNOSTIC)
	err := windows.RegNotifyChangeKeyValue(windows.Handle(key), false, filter, event, true)
	if err == nil {
		return nil
	}

	filter &^= windows.REG_NOTIFY_THREAD_AGNOSTIC
	if retryErr := windows.RegNotifyChangeKeyValue(windows.Handle(key), false, filter, event, true); retryErr != nil {
		return fmt.Errorf("监听代理设置注册表失败：%w", retryErr)
	}
	return nil
}

func closeHandles(handles []windows.Handle) {
	for _, handle := range handles {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}
}
