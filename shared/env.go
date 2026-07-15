package shared

import "strings"

// EnvMap converts an os.Environ()-style slice into a map for use with
// ConfigFromTransformContext. Plugins typically call this once at startup.
func EnvMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		out[key] = value
	}
	return out
}
