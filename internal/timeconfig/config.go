// Package timeconfig parses configuration values without changing clocks,
// resolving hosts, probing networks, or making authorization decisions.
package timeconfig

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

const (
	Automatic  = 0
	Manual     = 1
	dateLayout = "2006-01-02 15:04:05"
)

// Input preserves presence: nil means absent, including for NTPServers.
// An empty but non-nil NTPServers is a present field.
type Input struct {
	SyncType   *int
	Date       *string
	NTPServers []string
}

// Policy is explicit caller configuration, not historical product capacity.
// Location is required for manual mode; the parser never uses time.Local.
type Policy struct {
	MaxServers     int
	MaxServerBytes int
	Location       *time.Location
}

// Config is a value description only. ManualTime is UTC, with Timezone
// identifying the explicitly supplied location used to interpret ManualDate.
type Config struct {
	SyncType   int
	ManualDate string
	ManualTime time.Time
	Timezone   string
	NTPServers []string
}

// FieldError carries no user-provided value. Code and Field are stable.
type FieldError struct {
	Field string
	Code  string
}

// Parse returns the zero Config whenever any error occurs. Errors are ordered
// by policy fields, then mode, then date/timezone, then NTP input order.
func Parse(input Input, policy Policy) (Config, []FieldError) {
	var errors []FieldError
	add := func(field, code string) { errors = append(errors, FieldError{field, code}) }
	if policy.MaxServers < 1 || policy.MaxServers > 32 {
		add("policy.maxServers", "invalid_policy")
	}
	if policy.MaxServerBytes < 1 || policy.MaxServerBytes > 253 {
		add("policy.maxServerBytes", "invalid_policy")
	}
	if len(errors) != 0 {
		return Config{}, errors
	}
	if input.SyncType == nil {
		return Config{}, []FieldError{{"syncType", "required"}}
	}
	mode := *input.SyncType
	if mode != Automatic && mode != Manual {
		return Config{}, []FieldError{{"syncType", "invalid_mode"}}
	}
	result := Config{SyncType: mode}
	if mode == Manual {
		if input.Date == nil {
			add("date", "required")
		} else {
			wall, ok := parseDate(*input.Date)
			if !ok {
				add("date", "invalid_date")
			} else if validLocation(policy.Location) {
				instant, code := resolveWall(wall, policy.Location)
				if code != "" {
					add("date", code)
				} else {
					result.ManualDate = *input.Date
					result.ManualTime = instant.UTC()
					result.Timezone = policy.Location.String()
				}
			}
		}
		if !validLocation(policy.Location) {
			add("policy.location", "explicit_timezone_required")
		}
		if input.NTPServers != nil {
			add("ntpServers", "mode_conflict")
		}
	} else {
		if input.Date != nil {
			add("date", "mode_conflict")
		}
		if len(input.NTPServers) == 0 {
			add("ntpServers", "required")
		} else if len(input.NTPServers) > policy.MaxServers {
			add("ntpServers", "too_many")
		} else {
			seen := make(map[string]bool, len(input.NTPServers))
			for index, raw := range input.NTPServers {
				field := fmt.Sprintf("ntpServers[%d]", index)
				if len(raw) > policy.MaxServerBytes {
					add(field, "too_long")
					continue
				}
				host, ok := normalizeHost(raw)
				if !ok {
					add(field, "invalid_host")
					continue
				}
				if seen[host] {
					add(field, "duplicate")
					continue
				}
				seen[host] = true
				result.NTPServers = append(result.NTPServers, host)
			}
		}
	}
	if len(errors) != 0 {
		return Config{}, errors
	}
	return result, nil
}

func validLocation(location *time.Location) bool {
	return location != nil && location != time.Local && location.String() != "Local"
}

func parseDate(raw string) (time.Time, bool) {
	if len(raw) != 19 {
		return time.Time{}, false
	}
	for i := 0; i < len(raw); i++ {
		switch i {
		case 4, 7:
			if raw[i] != '-' {
				return time.Time{}, false
			}
		case 10:
			if raw[i] != ' ' {
				return time.Time{}, false
			}
		case 13, 16:
			if raw[i] != ':' {
				return time.Time{}, false
			}
		default:
			if raw[i] < '0' || raw[i] > '9' {
				return time.Time{}, false
			}
		}
	}
	parsed, err := time.Parse(dateLayout, raw)
	return parsed, err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999
}

func normalizeHost(raw string) (string, bool) {
	if raw == "" {
		return "", false
	}
	if address, err := netip.ParseAddr(raw); err == nil {
		if address.Zone() != "" {
			return "", false
		}
		return address.Unmap().String(), true
	}
	name := strings.TrimSuffix(raw, ".")
	if len(name) > 253 {
		return "", false
	}
	labels := strings.Split(name, ".")
	if len(labels) < 2 {
		return "", false
	}
	allNumeric := true
	for _, label := range labels {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c < '0' || c > '9' {
				allNumeric = false
			}
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return "", false
			}
		}
	}
	// Do not reinterpret an invalid or abbreviated numeric IP as DNS.
	if allNumeric {
		return "", false
	}
	return strings.ToLower(name), true
}
