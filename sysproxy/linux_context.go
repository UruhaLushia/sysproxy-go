//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

type linuxExecContext struct {
	env           []string
	envMap        map[string]string
	useCredential bool
	uid           uint32
	gid           uint32
}

func newLinuxExecContext(opt *Options) (*linuxExecContext, error) {
	envMap := currentProcessEnv()
	ctx := &linuxExecContext{
		envMap: envMap,
		uid:    uint32(os.Getuid()),
		gid:    uint32(os.Getgid()),
	}

	if opt != nil && opt.PeerPID > 0 {
		peerEnv, err := readProcessEnv(opt.PeerPID)
		if err != nil {
			return nil, fmt.Errorf("读取连接进程环境失败：%w", err)
		}

		envMap = mergeEnvMaps(sessionBaseEnv(), peerEnv)
		ctx.envMap = envMap
		ctx.uid = opt.PeerUID
		ctx.gid = opt.PeerGID
		ctx.useCredential = os.Geteuid() == 0
		ensureLinuxSessionEnv(envMap, ctx.uid)
	}

	ctx.env = envMapToSlice(ctx.envMap)
	return ctx, nil
}

func currentProcessEnv() map[string]string {
	return envSliceToMap(os.Environ())
}

func sessionBaseEnv() map[string]string {
	env := map[string]string{}
	for _, key := range []string{
		"PATH",
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"LC_MESSAGES",
		"TERM",
	} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	return env
}

func mergeEnvMaps(base, override map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range override {
		merged[key] = value
	}
	return merged
}

func envSliceToMap(env []string) map[string]string {
	envMap := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}
	return envMap
}

func envMapToSlice(envMap map[string]string) []string {
	env := make([]string, 0, len(envMap))
	for key, value := range envMap {
		env = append(env, key+"="+value)
	}
	return env
}

func ensureLinuxSessionEnv(envMap map[string]string, uid uint32) {
	if envMap["XDG_RUNTIME_DIR"] == "" {
		envMap["XDG_RUNTIME_DIR"] = filepath.Join("/run/user", fmt.Sprintf("%d", uid))
	}
	if envMap["DBUS_SESSION_BUS_ADDRESS"] == "" && envMap["XDG_RUNTIME_DIR"] != "" {
		envMap["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + filepath.Join(envMap["XDG_RUNTIME_DIR"], "bus")
	}
}

func readProcessEnv(pid int) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", fmt.Sprintf("%d", pid), "environ"))
	if err != nil {
		return nil, err
	}

	envMap := map[string]string{}
	for _, item := range strings.Split(string(data), "\x00") {
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}
	return envMap, nil
}

func execAsCurrentUser(ctx *linuxExecContext, name string, arg ...string) *exec.Cmd {
	cmd := exec.Command(name, arg...)
	if ctx != nil {
		cmd.Env = ctx.env
		if ctx.useCredential {
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Credential: &syscall.Credential{
					Uid: ctx.uid,
					Gid: ctx.gid,
				},
			}
		}
	}
	return cmd
}
