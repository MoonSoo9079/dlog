package agent

import (
	"os"
	"regexp"
	"strings"
)

var cgroupID = regexp.MustCompile(`[0-9a-fA-F]{64}`)

func ownContainerID() string {
	var cgroup strings.Builder
	for _, p := range []string{"/proc/self/cgroup", "/proc/1/cgroup"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		cgroup.Write(b)
		cgroup.WriteByte('\n')
	}
	mount := ""
	if inContainer() {
		if b, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
			mount = string(b)
		}
	}
	return containerIDFrom(cgroup.String(), mount)
}

// inContainer reports whether this process is inside a container.
// A host process can see other containers' ids in mountinfo (rootfs/overlayfs/<id>),
// so that file is not a safe place to learn our own id unless we are actually inside one.
func inContainer() bool {
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := strings.TrimSpace(string(b))
	return s == "0::/" || s == "0::"
}

// containerIDFrom returns the container id from cgroup text.
// mountinfo is only consulted when it was already decided that this process is inside a container.
func containerIDFrom(cgroup, mountinfo string) string {
	if m := cgroupID.FindString(cgroup); m != "" {
		return strings.ToLower(m)
	}
	if mountinfo == "" {
		return ""
	}
	if m := cgroupID.FindString(mountinfo); m != "" {
		return strings.ToLower(m)
	}
	return ""
}

func sameContainer(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if a == b {
		return true
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= 12 && strings.HasPrefix(long, short)
}
