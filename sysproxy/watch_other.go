//go:build !windows && !linux && !darwin

package sysproxy

import "context"

func WaitProxySettingsChange(ctx context.Context, _ *Options) error {
	return WaitProxySettingsChangeReady(ctx, nil, nil)
}

func WaitProxySettingsChangeReady(ctx context.Context, _ *Options, ready func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ready != nil {
		ready()
	}
	<-ctx.Done()
	return ctx.Err()
}
