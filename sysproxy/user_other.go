//go:build !linux && !windows

package sysproxy

import (
	"fmt"
	"runtime"
)

func OptionsForUser(_ string) (*Options, error) {
	return nil, fmt.Errorf("%s 不支持指定用户", runtime.GOOS)
}

func OptionsForProcess(_ int) (*Options, error) {
	return nil, fmt.Errorf("%s 不支持指定进程", runtime.GOOS)
}
