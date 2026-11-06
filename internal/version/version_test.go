package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClosestTag(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init")
	writeFile(t, filepath.Join(dir, "f"), "a\n")
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-m", "a")
	if got := closest(dir); got != "" {
		t.Fatalf("no tag: %q", got)
	}

	git(t, dir, "tag", "v0.1.0")
	if got := closest(dir); got != "0.1.0" {
		t.Fatalf("v0.1.0: %q", got)
	}
	if got := scriptVersion(t, dir); got != "0.1.0" {
		t.Fatalf("script v0.1.0: %q", got)
	}

	writeFile(t, filepath.Join(dir, "f"), "b\n")
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-m", "b")
	if got := closest(dir); got != "0.1.0" {
		t.Fatalf("ancestor: %q", got)
	}

	git(t, dir, "tag", "0.2.0")
	if got := closest(dir); got != "0.2.0" {
		t.Fatalf("0.2.0: %q", got)
	}
	if got := scriptVersion(t, dir); got != "0.2.0" {
		t.Fatalf("script 0.2.0: %q", got)
	}

	writeFile(t, filepath.Join(dir, "f"), "c\n")
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-m", "c")
	git(t, dir, "tag", "v0.3.0-rc.1")
	if got := closest(dir); got != "0.3.0-rc.1" {
		t.Fatalf("prerelease: %q", got)
	}
}

func TestClosestIgnoresOtherTags(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init")
	writeFile(t, filepath.Join(dir, "f"), "a\n")
	git(t, dir, "add", "f")
	git(t, dir, "commit", "-m", "a")
	git(t, dir, "tag", "wip")
	if got := closest(dir); got != "" {
		t.Fatalf("wip: %q", got)
	}
	if got := scriptVersion(t, dir); got != "0.1.0" {
		t.Fatalf("script fallback: %q", got)
	}
}

func scriptVersion(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("sh", closestScript(t))
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	return string(out)
}

func closestScript(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "closest.sh")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
