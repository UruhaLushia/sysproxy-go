//go:build !windows && !linux && !darwin

package sysproxy

import "context"

func WaitProxySettingsChange(ctx context.Context, _ *Options) error {
	if ctx == nil {
		ctx = context.Background()
	}
	<-ctx.Done()
	return ctx.Err()
}
