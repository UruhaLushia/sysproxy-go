//go:build linux

package sysproxy

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

func OptionsForUser(name string) (*Options, error) {
	usr, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("查找用户失败：%w", err)
	}

	uid, err := parseLinuxUserID(usr.Uid, "uid")
	if err != nil {
		return nil, err
	}
	gid, err := parseLinuxUserID(usr.Gid, "gid")
	if err != nil {
		return nil, err
	}

	envMap, ok := findLinuxUserSessionEnv(uid)
	if !ok {
		envMap = linuxUserFallbackEnv(usr, uid)
	}
	ensureLinuxSessionEnv(envMap, uid)

	return &Options{
		PeerUID:     uid,
		PeerGID:     gid,
		Environment: envMapToSlice(envMap),
	}, nil
}

func OptionsForProcess(pid int) (*Options, error) {
	if pid <= 0 {
		return nil, fmt.Errorf("PID 无效：%d", pid)
	}

	envMap, err := readProcessEnv(pid)
	if err != nil {
		return nil, fmt.Errorf("读取进程环境失败：%w", err)
	}
	uid, gid, err := readProcessOwner(pid)
	if err != nil {
		return nil, fmt.Errorf("读取进程 owner 失败：%w", err)
	}

	envMap = mergeEnvMaps(sessionBaseEnv(), envMap)
	ensureLinuxSessionEnv(envMap, uid)
	return &Options{
		PeerUID:     uid,
		PeerGID:     gid,
		Environment: envMapToSlice(envMap),
	}, nil
}

func parseLinuxUserID(value, name string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("解析用户 %s 失败：%w", name, err)
	}
	return uint32(id), nil
}

func findLinuxUserSessionEnv(uid uint32) (map[string]string, bool) {
	if uint32(os.Getuid()) == uid {
		envMap := currentProcessEnv()
		if scoreLinuxSessionEnv(envMap) > 0 {
			return envMap, true
		}
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}

	var best map[string]string
	bestScore := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		procUID, _, err := readProcessOwner(pid)
		if err != nil || procUID != uid {
			continue
		}
		envMap, err := readProcessEnv(pid)
		if err != nil {
			continue
		}
		score := scoreLinuxSessionEnv(envMap)
		if score > bestScore {
			best = envMap
			bestScore = score
		}
	}

	return best, bestScore > 0
}

func scoreLinuxSessionEnv(envMap map[string]string) int {
	score := 0
	if envMap["XDG_CURRENT_DESKTOP"] != "" {
		score += 8
	}
	if envMap["DBUS_SESSION_BUS_ADDRESS"] != "" {
		score += 4
	}
	if envMap["WAYLAND_DISPLAY"] != "" || envMap["DISPLAY"] != "" {
		score += 2
	}
	if envMap["XDG_RUNTIME_DIR"] != "" {
		score++
	}
	if envMap["HOME"] != "" {
		score++
	}
	return score
}

func linuxUserFallbackEnv(usr *user.User, uid uint32) map[string]string {
	envMap := sessionBaseEnv()
	if usr.HomeDir != "" {
		envMap["HOME"] = usr.HomeDir
	}
	if usr.Username != "" {
		envMap["USER"] = usr.Username
		envMap["LOGNAME"] = usr.Username
	}
	if envMap["XDG_CURRENT_DESKTOP"] == "" && uint32(os.Getuid()) == uid {
		if desktop := os.Getenv("XDG_CURRENT_DESKTOP"); desktop != "" {
			envMap["XDG_CURRENT_DESKTOP"] = desktop
		}
	}
	return envMap
}

func readProcessOwner(pid int) (uint32, uint32, error) {
	info, err := os.Stat(filepath.Join("/proc", fmt.Sprintf("%d", pid)))
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("无法读取进程 owner")
	}
	return stat.Uid, stat.Gid, nil
}
