package systemhealth

import (
	"bytes"
	"errors"
	"math"
	"strconv"
	"strings"
)

var errInvalidData = errors.New("invalid kernel measurement")

type cpuObservation struct {
	counters [8]uint64
	count    int
	total    uint64
}

func parseCPU(data []byte) (cpuObservation, error) {
	var result cpuObservation
	line, _, _ := bytes.Cut(data, []byte{'\n'})
	fields := strings.Fields(string(line))
	// Linux currently exports 10 counters; early kernels exported only 4.
	// Guest and guest_nice are already included in user/nice and never summed.
	if len(fields) < 5 || len(fields) > 11 || fields[0] != "cpu" {
		return result, errInvalidData
	}
	result.count = len(fields) - 1
	for i, field := range fields[1:] {
		value, err := unsigned(field)
		if err != nil {
			return cpuObservation{}, err
		}
		if i < len(result.counters) {
			if value > math.MaxUint64-result.total {
				return cpuObservation{}, errInvalidData
			}
			result.counters[i] = value
			result.total += value
		}
	}
	return result, nil
}

func parseMemory(data []byte) (total, available uint64, reason string) {
	foundTotal, foundAvailable := false, false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "MemTotal:" && fields[0] != "MemAvailable:" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return 0, 0, "invalid_data"
		}
		value, err := unsigned(fields[1])
		if err != nil || value > math.MaxUint64/1024 {
			return 0, 0, "invalid_data"
		}
		if fields[0] == "MemTotal:" {
			if foundTotal {
				return 0, 0, "invalid_data"
			}
			total, foundTotal = value*1024, true
		} else {
			if foundAvailable {
				return 0, 0, "invalid_data"
			}
			available, foundAvailable = value*1024, true
		}
	}
	if !foundTotal {
		return 0, 0, "missing_total"
	}
	if !foundAvailable {
		return 0, 0, "missing_available"
	}
	if total == 0 || available > total {
		return 0, 0, "invalid_data"
	}
	return total, available, ""
}

func parseUptime(data []byte) (uint64, error) {
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, errInvalidData
	}
	for _, field := range fields {
		if _, err := nonnegativeDecimal(field); err != nil {
			return 0, err
		}
	}
	whole, _, _ := strings.Cut(fields[0], ".")
	seconds, err := unsigned(whole)
	// Keep the public JSON integer exact in JavaScript clients. This exceeds
	// any practical kernel uptime (285 million years), without float casts.
	if err != nil || seconds > 1<<53-1 {
		return 0, errInvalidData
	}
	return seconds, nil
}

func parseLoad(data []byte) (LoadAverages, error) {
	var result LoadAverages
	fields := strings.Fields(string(data))
	if len(fields) != 5 {
		return result, errInvalidData
	}
	values := []*float64{&result.One, &result.Five, &result.Fifteen}
	for i, target := range values {
		value, err := nonnegativeDecimal(fields[i])
		if err != nil || value > math.MaxUint32 {
			return LoadAverages{}, errInvalidData
		}
		*target = value
	}
	runnable, tasks, found := strings.Cut(fields[3], "/")
	running, runningErr := unsigned(runnable)
	total, totalErr := unsigned(tasks)
	lastPID, pidErr := unsigned(fields[4])
	if !found || runningErr != nil || totalErr != nil || pidErr != nil || total == 0 ||
		running > total || total > math.MaxUint32 || lastPID > math.MaxInt32 {
		return LoadAverages{}, errInvalidData
	}
	return result, nil
}

func unsigned(value string) (uint64, error) {
	if len(value) == 0 || len(value) > 20 {
		return 0, errInvalidData
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return 0, errInvalidData
		}
	}
	result, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, errInvalidData
	}
	return result, nil
}

func nonnegativeDecimal(value string) (float64, error) {
	if len(value) == 0 || len(value) > 64 {
		return 0, errInvalidData
	}
	whole, fraction, hasDot := strings.Cut(value, ".")
	if len(whole) == 0 || hasDot && len(fraction) == 0 {
		return 0, errInvalidData
	}
	for _, part := range []string{whole, fraction} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, errInvalidData
			}
		}
	}
	result, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(result) || math.IsInf(result, 0) || result < 0 {
		return 0, errInvalidData
	}
	return result, nil
}
