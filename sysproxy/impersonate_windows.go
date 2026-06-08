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

func OptionsForUser(name string) (*Options, error) {
	sid, err := lookupAccountSID(name)
	if err != nil {
		return nil, err
	}
	return &Options{UserSID: sid}, nil
}

func OptionsForProcess(pid int) (*Options, error) {
	sid, err := processUserSID(pid)
	if err != nil {
		return nil, fmt.Errorf("解析进程用户 SID 失败：%w", err)
	}
	return &Options{UserSID: sid}, nil
}

func openCurrentUserKey(path string, access uint32) (registry.Key, error) {
	var currentUser registry.Key
	if ret, _, _ := procRegOpenCurrentUser.Call(uintptr(access), uintptr(unsafe.Pointer(&currentUser))); ret != 0 {
		return 0, syscall.Errno(ret)
	}
	defer currentUser.Close()

	return registry.OpenKey(currentUser, path, access)
}

func openOptionsUserKey(path string, access uint32, opt *Options) (registry.Key, error) {
	sid, err := resolveOptionsUserSID(opt)
	if err != nil {
		return 0, err
	}
	if sid == "" {
		return openCurrentUserKey(path, access)
	}
	return registry.OpenKey(registry.USERS, sid+`\`+path, access)
}

func resolveOptionsUserSID(opt *Options) (string, error) {
	if opt == nil {
		return "", nil
	}
	return opt.UserSID, nil
}

func lookupAccountSID(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("用户不能为空")
	}

	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}

	var sidLen uint32
	var domainLen uint32
	var use uint32
	err = windows.LookupAccountName(nil, namePtr, nil, &sidLen, nil, &domainLen, &use)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		if err == nil {
			return "", fmt.Errorf("解析用户 SID 失败：返回空 SID")
		}
		return "", err
	}

	sidBuffer := make([]byte, sidLen)
	sid := (*windows.SID)(unsafe.Pointer(&sidBuffer[0]))
	domainBuffer := make([]uint16, domainLen)
	var domainPtr *uint16
	if len(domainBuffer) > 0 {
		domainPtr = &domainBuffer[0]
	}
	if err := windows.LookupAccountName(nil, namePtr, sid, &sidLen, domainPtr, &domainLen, &use); err != nil {
		return "", err
	}

	return sid.String(), nil
}

func processUserSID(pid int) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(process)

	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	if user == nil || user.User.Sid == nil {
		return "", fmt.Errorf("进程令牌没有用户 SID")
	}
	return user.User.Sid.String(), nil
}
