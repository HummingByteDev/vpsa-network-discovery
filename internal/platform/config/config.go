// Package config loads service configuration from CNIP_-prefixed environment
// variables and can dump the effective configuration with secrets redacted.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const prefix = "CNIP_"

// Source accumulates lookups so the effective config can be dumped at boot and
// missing required keys reported as one error.
type Source struct {
	seen    map[string]string
	missing []string
}

func New() *Source {
	return &Source{seen: map[string]string{}}
}

func (s *Source) lookup(key, def string) string {
	v, ok := os.LookupEnv(prefix + key)
	if !ok {
		v = def
	}
	s.seen[key] = v
	return v
}

func (s *Source) String(key, def string) string { return s.lookup(key, def) }

func (s *Source) Require(key string) string {
	v := s.lookup(key, "")
	if v == "" {
		s.missing = append(s.missing, prefix+key)
	}
	return v
}

func (s *Source) Int(key string, def int) int {
	v := s.lookup(key, strconv.Itoa(def))
	n, err := strconv.Atoi(v)
	if err != nil {
		s.missing = append(s.missing, fmt.Sprintf("%s%s (not an integer: %q)", prefix, key, v))
		return def
	}
	return n
}

func (s *Source) Bool(key string, def bool) bool {
	v := s.lookup(key, strconv.FormatBool(def))
	b, err := strconv.ParseBool(v)
	if err != nil {
		s.missing = append(s.missing, fmt.Sprintf("%s%s (not a bool: %q)", prefix, key, v))
		return def
	}
	return b
}

func (s *Source) Duration(key string, def time.Duration) time.Duration {
	v := s.lookup(key, def.String())
	d, err := time.ParseDuration(v)
	if err != nil {
		s.missing = append(s.missing, fmt.Sprintf("%s%s (not a duration: %q)", prefix, key, v))
		return def
	}
	return d
}

// Err reports all missing/invalid keys recorded so far.
func (s *Source) Err() error {
	if len(s.missing) == 0 {
		return nil
	}
	return fmt.Errorf("configuration errors: %s", strings.Join(s.missing, ", "))
}

var secretMarkers = []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "DSN", "CREDENTIAL"}

func redacted(key string) bool {
	k := strings.ToUpper(key)
	for _, m := range secretMarkers {
		if strings.Contains(k, m) {
			return true
		}
	}
	return false
}

// Dump logs the effective configuration, redacting secret-like keys.
func (s *Source) Dump(log *slog.Logger) {
	keys := make([]string, 0, len(s.seen))
	for k := range s.seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	attrs := make([]any, 0, len(keys))
	for _, k := range keys {
		v := s.seen[k]
		if redacted(k) && v != "" {
			v = "[redacted]"
		}
		attrs = append(attrs, slog.String(prefix+k, v))
	}
	log.Info("effective configuration", attrs...)
}
