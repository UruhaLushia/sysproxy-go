//go:build !darwin && !linux && !windows

package sysproxy

import "fmt"

func DisableProxy(_ *Options) error {
	return fmt.Errorf("不支持的操作系统")
}

func SetProxy(_ *Options) error {
	return fmt.Errorf("不支持的操作系统")
}

func SetPac(_ *Options) error {
	return fmt.Errorf("不支持的操作系统")
}

func QueryProxySettings(_ *Options) (map[string]any, error) {
	return nil, fmt.Errorf("不支持的操作系统")
}
