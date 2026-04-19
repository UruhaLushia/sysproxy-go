package sysproxy

import "runtime"

type Options struct {
	Proxy            string
	Bypass           string
	PACURL           string
	Device           string
	OnlyActiveDevice bool
	PeerPID          int
	PeerUID          uint32
	PeerGID          uint32
	Concurrent       *bool
}

func resolveConcurrentApply(opt *Options) bool {
	if opt != nil && opt.Concurrent != nil {
		return *opt.Concurrent
	}
	return DefaultConcurrent()
}

func DefaultConcurrent() bool {
	return runtime.GOOS == "darwin"
}
