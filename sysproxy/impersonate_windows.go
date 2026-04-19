//go:build windows

package sysproxy

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	advapi32                       = windows.NewLazySystemDLL("advapi32.dll")
	procImpersonateNamedPipeClient = advapi32.NewProc("ImpersonateNamedPipeClient")
	procRegOpenCurrentUser         = advapi32.NewProc("RegOpenCurrentUser")
)

func RunAsNamedPipeClient(pipe windows.Handle, fn func() error) (err error) {
	if pipe == 0 {
		return fmt.Errorf("命名管道句柄无效")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if ret, _, callErr := procImpersonateNamedPipeClient.Call(uintptr(pipe)); ret == 0 {
		return fmt.Errorf("模拟命名管道客户端失败：%v", callErr)
	}

	defer func() {
		if revertErr := windows.RevertToSelf(); err == nil && revertErr != nil {
			err = fmt.Errorf("恢复线程身份失败：%w", revertErr)
		}
	}()

	return fn()
}

func openCurrentUserKey(path string, access uint32) (registry.Key, error) {
	var currentUser registry.Key
	if ret, _, _ := procRegOpenCurrentUser.Call(uintptr(access), uintptr(unsafe.Pointer(&currentUser))); ret != 0 {
		return 0, syscall.Errno(ret)
	}
	defer currentUser.Close()

	return registry.OpenKey(currentUser, path, access)
}
