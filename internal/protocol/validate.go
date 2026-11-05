package protocol

import "regexp"

var (
	hostnameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	containerIDRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

func ValidHostname(s string) bool {
	return hostnameRE.MatchString(s)
}

func ValidContainerID(s string) bool {
	return containerIDRE.MatchString(s)
}

// CleanName reports whether name is safe to store and display as text.
func CleanName(s string) bool {
	if s == "" || len(s) > MaxName {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
