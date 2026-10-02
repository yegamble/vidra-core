package main

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// statEnvOwner reads who owns the env file. syscall.Stat_t is unix-only, which
// costs nothing: the release builds linux and darwin, and the package already
// cannot build elsewhere (internal/diskspace uses syscall.Statfs).
func statEnvOwner(path string) envOwner {
	info, err := os.Stat(path)
	if err != nil {
		return envOwner{}
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return envOwner{}
	}
	uid := strconv.FormatUint(uint64(st.Uid), 10)
	name := uid // an LDAP/container uid with no passwd entry still gets named
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		name = u.Username
	}
	return envOwner{name: name, known: true, self: int(st.Uid) == os.Getuid()}
}
