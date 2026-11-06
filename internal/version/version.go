// Package version reports the closest reachable version tag.
package version

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Version is the closest reachable version tag, without a leading v.
// A build stamps this with -ldflags -X. When this repository is available,
// init replaces the stamp with the tag git can see.
var Version = "0.1.0"

var versionTag = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$`)

func init() {
	if v := fromModule(); v != "" {
		Version = v
	}
}

func fromModule() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	root := strings.TrimSpace(string(out))
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !dlogModule(mod) {
		return ""
	}
	return closest(root)
}

func dlogModule(mod []byte) bool {
	for _, line := range bytes.Split(mod, []byte("\n")) {
		if bytes.Equal(bytes.TrimSpace(line), []byte("module dlog")) {
			return true
		}
	}
	return false
}

// closest returns the nearest ancestor tag that looks like a version.
// The leading v is removed. An empty result means git has no such tag.
func closest(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "safe.directory=*", "-C", dir, "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*.[0-9]*.[0-9]*", "--match", "[0-9]*.[0-9]*.[0-9]*")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	tag := strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
	if !versionTag.MatchString(tag) {
		return ""
	}
	return tag
}
