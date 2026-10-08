package systemhealth

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestParseCPUCounterBoundaries(t *testing.T) {
	for _, source := range []string{
		"", "cpu", "cpu 1 2 3", "cpu0 1 2 3 4", "intr 1 2 3 4", "cpu -1 2 3 4", "cpu +1 2 3 4",
		"cpu 1.0 2 3 4", "cpu 18446744073709551616 0 0 0", "cpu 18446744073709551615 1 0 0",
		"cpu 1 2 3 4 0 0 0 0 NaN 0", "cpu 1 2 3 4 0 0 0 0 0 0 1", "cpu 0x1 2 3 4",
	} {
		if _, err := parseCPU([]byte(source)); err == nil {
			t.Errorf("accepted malformed CPU input %q", source)
		}
	}
	for _, source := range []string{"cpu 1 2 3 4", "cpu 1 2 3 4 0 0 0 0 18446744073709551615 18446744073709551615"} {
		observation, err := parseCPU([]byte(source))
		if err != nil || observation.total != 10 {
			t.Fatalf("guest counters double-counted or valid counters rejected: %+v, %v", observation, err)
		}
	}
	observation, err := parseCPU([]byte("cpu 18446744073709551615 0 0 0"))
	if err != nil || observation.total != math.MaxUint64 {
		t.Fatal("exact uint64 counter bound failed")
	}
}

func TestMemoryUsesAvailableWithoutFallback(t *testing.T) {
	tests := []struct{ name, source, reason string }{
		{"missing available", "MemTotal: 100 kB\nMemFree: 20 kB\nBuffers: 30 kB\nCached: 40 kB", "missing_available"},
		{"missing total", "MemAvailable: 10 kB", "missing_total"},
		{"duplicate total", "MemTotal: 100 kB\nMemTotal: 100 kB\nMemAvailable: 50 kB", "invalid_data"},
		{"duplicate available", "MemTotal: 100 kB\nMemAvailable: 50 kB\nMemAvailable: 50 kB", "invalid_data"},
		{"missing units", "MemTotal: 100\nMemAvailable: 50 kB", "invalid_data"},
		{"wrong units", "MemTotal: 100 MB\nMemAvailable: 50 kB", "invalid_data"},
		{"zero total", "MemTotal: 0 kB\nMemAvailable: 0 kB", "invalid_data"},
		{"negative", "MemTotal: 100 kB\nMemAvailable: -1 kB", "invalid_data"},
		{"over total", "MemTotal: 100 kB\nMemAvailable: 101 kB", "invalid_data"},
		{"nonfinite", "MemTotal: NaN kB\nMemAvailable: 50 kB", "invalid_data"},
		{"overflow bytes", "MemTotal: 18014398509481984 kB\nMemAvailable: 0 kB", "invalid_data"},
		{"overflow integer", "MemTotal: 18446744073709551616 kB\nMemAvailable: 0 kB", "invalid_data"},
		{"trailing tokens", "MemTotal: 100 kB ignored\nMemAvailable: 50 kB", "invalid_data"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f, sampler := newFixture(t)
			f.data[ProcMeminfo] = test.source
			memory := sampler.Sample().Memory
			if memory.Availability != Unavailable || memory.Reason != test.reason || memory.Percent != nil || memory.TotalBytes != nil || memory.AvailableBytes != nil || memory.UsedBytes != nil {
				t.Fatalf("invalid RAM input produced a value: %+v", memory)
			}
		})
	}
	for _, available := range []int{0, 100} {
		f, sampler := newFixture(t)
		f.data[ProcMeminfo] = fmt.Sprintf("MemTotal: 100 kB\nMemAvailable: %d kB\n", available)
		requirePercent(t, sampler.Sample().Memory.Percent, float64(100-available))
	}
}

func TestParseUptimeBoundaries(t *testing.T) {
	for _, source := range []string{"", "1", "1 2 3", "-1 0", "1 -2", "NaN 0", "Inf 0", "1e3 0", "+1 0", ".2 0", "1. 0", "1..2 0", "9007199254740992 0", "18446744073709551616 0", strings.Repeat("9", 65) + " 0"} {
		if _, err := parseUptime([]byte(source)); err == nil {
			t.Errorf("accepted invalid uptime %q", source)
		}
	}
	for _, test := range []struct {
		source string
		want   uint64
	}{{"0.00 0.00", 0}, {"123.99 1000.50", 123}, {"9007199254740991.99 0", 1<<53 - 1}} {
		value, err := parseUptime([]byte(test.source))
		if err != nil || value != test.want {
			t.Errorf("uptime %q = %d, %v; want %d", test.source, value, err, test.want)
		}
	}
}

func TestParseLoadBoundaries(t *testing.T) {
	for _, source := range []string{
		"", "1 2 3", "1 2 3 1/2", "1 2 3 1/2 3 extra", "NaN 2 3 1/2 3", "Inf 2 3 1/2 3", "-1 2 3 1/2 3",
		"1e3 2 3 1/2 3", "+1 2 3 1/2 3", "1 2 3 3/2 3", "1 2 3 0/0 3", "1 2 3 1/2/3 3",
		"1 2 3 -1/2 3", "1 2 3 1/2 -1", "1 2 3 1/2 2147483648", "1 2 3 1/4294967296 3", "4294967296 2 3 1/2 3",
	} {
		if _, err := parseLoad([]byte(source)); err == nil {
			t.Errorf("accepted malformed load %q", source)
		}
	}
	values, err := parseLoad([]byte("0 1.25 2.50 0/100 0\n"))
	if err != nil || values != (LoadAverages{One: 0, Five: 1.25, Fifteen: 2.5}) {
		t.Fatalf("valid load rejected: %+v, %v", values, err)
	}
}

func FuzzKernelParsers(f *testing.F) {
	for _, source := range []string{"cpu 1 2 3 4", "MemTotal: 100 kB\nMemAvailable: 25 kB", "1.25 2.50", "1 2 3 1/2 3", "NaN", ""} {
		f.Add(source)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > maxProcBytes {
			return
		}
		if value, err := parseCPU([]byte(source)); err == nil {
			var total uint64
			for _, counter := range value.counters {
				if counter > math.MaxUint64-total {
					t.Fatal("CPU overflow accepted")
				}
				total += counter
			}
			if total != value.total {
				t.Fatal("CPU total disagrees with counters")
			}
		}
		if total, available, reason := parseMemory([]byte(source)); reason == "" && (total == 0 || available > total) {
			t.Fatal("invalid memory accepted")
		}
		if value, err := parseUptime([]byte(source)); err == nil && value > 1<<53-1 {
			t.Fatal("inexact public uptime integer")
		}
		if value, err := parseLoad([]byte(source)); err == nil {
			for _, number := range []float64{value.One, value.Five, value.Fifteen} {
				if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > math.MaxUint32 {
					t.Fatal("invalid public load value")
				}
			}
		}
	})
}
