package config

import (
	"bufio"
	"os"
	"strings"
)

// loadDotEnv reads a simple .env file (KEY=VALUE lines, # comments and blank
// lines ignored, single/double quotes stripped) into a map. Missing file or
// unreadable path is not an error: the process environment still wins.
func loadDotEnv(path string) map[string]string {
	out := make(map[string]string)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"'`)
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// LookupWithDotEnv returns an EnvLookup that prefers the live process
// environment and falls back to values loaded from path. Explicit exports
// always override the file.
func LookupWithDotEnv(path string) (EnvLookup, map[string]string) {
	fileVars := loadDotEnv(path)
	return func(key string) (string, bool) {
		if v, ok := os.LookupEnv(key); ok {
			return v, true
		}
		v, ok := fileVars[key]
		return v, ok
	}, fileVars
}
