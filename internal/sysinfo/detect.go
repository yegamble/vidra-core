package sysinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

type Resources struct {
	CPUCores int
	RAMBytes int64
	RAMGB    float64
}

func DetectResources() *Resources {
	cpu := DetectCPU()
	ram := DetectRAM()

	return &Resources{
		CPUCores: cpu,
		RAMBytes: ram,
		RAMGB:    float64(ram) / (1024 * 1024 * 1024),
	}
}

func DetectCPU() int {
	if cores := readCgroupV2CPU(); cores > 0 {
		return cores
	}

	if cores := readCgroupV1CPU(); cores > 0 {
		return cores
	}

	return runtime.NumCPU()
}

func DetectRAM() int64 {
	if ram := readCgroupV2Memory(); ram > 0 {
		return ram
	}

	if ram := readCgroupV1Memory(); ram > 0 {
		return ram
	}

	if ram := readProcMeminfo(); ram > 0 {
		return ram
	}

	return 1024 * 1024 * 1024
}

func readCgroupV2CPU() int {
	data, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0
	}

	parts := strings.Fields(string(data))
	if len(parts) < 2 || parts[0] == "max" {
		return 0
	}

	quota, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0
	}

	period, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0
	}

	cores := int(quota / period)
	if cores < 1 {
		cores = 1
	}
	return cores
}

func readCgroupV1CPU() int {
	quotaData, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	if err != nil {
		return 0
	}

	quota, err := strconv.ParseInt(strings.TrimSpace(string(quotaData)), 10, 64)
	if err != nil || quota <= 0 {
		return 0
	}

	periodData, err := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if err != nil {
		return 0
	}

	period, err := strconv.ParseInt(strings.TrimSpace(string(periodData)), 10, 64)
	if err != nil || period <= 0 {
		return 0
	}

	cores := int(quota / period)
	if cores < 1 {
		cores = 1
	}
	return cores
}

func readCgroupV2Memory() int64 {
	data, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return 0
	}

	limit := strings.TrimSpace(string(data))
	if limit == "max" {
		return 0
	}

	bytes, err := strconv.ParseInt(limit, 10, 64)
	if err != nil {
		return 0
	}

	return bytes
}

func readCgroupV1Memory() int64 {
	data, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if err != nil {
		return 0
	}

	bytes, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}

	if bytes > (1 << 62) {
		return 0
	}

	return bytes
}

func readProcMeminfo() int64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				continue
			}
			return kb * 1024
		}
	}

	return 0
}
