// Package monitor 通过 SSH 采集 CPU、内存、存储、网络的一次性快照。
//
// 采集方式是执行一段只读 shell 脚本并解析输出，不写入远端、不常驻轮询。
package monitor

import (
	"errors"
	"strconv"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/huoguoji/shellcove/internal/apperr"
)

// CollectTimeout 采集命令整体超时时间。
const CollectTimeout = 20 * time.Second

// 采集相关错误码。
const (
	codeSessionFailed = "MONITOR_SESSION_FAILED"
	codeCollectFailed = "MONITOR_FAILED"
	codeTimeout       = "MONITOR_TIMEOUT"
)

// IsTimeout 判断错误是否为采集超时。超时说明远端响应过慢，重试通常没有意义。
func IsTimeout(err error) bool { return apperr.Is(err, codeTimeout) }

// CPU CPU 使用情况快照。
type CPU struct {
	Cores        int     `json:"cores"`
	UsagePercent float64 `json:"usage_percent"`
	Load1        float64 `json:"load1"`
	Load5        float64 `json:"load5"`
	Load15       float64 `json:"load15"`
	UptimeSecond float64 `json:"uptime_seconds"`
}

// Memory 内存快照（单位 KB）。
type Memory struct {
	TotalKB     int64   `json:"total_kb"`
	UsedKB      int64   `json:"used_kb"`
	AvailableKB int64   `json:"available_kb"`
	FreeKB      int64   `json:"free_kb"`
	UsedPercent float64 `json:"used_percent"`
	SwapTotalKB int64   `json:"swap_total_kb"`
	SwapUsedKB  int64   `json:"swap_used_kb"`
}

// Disk 磁盘分区快照（单位 KB）。
type Disk struct {
	Filesystem string  `json:"filesystem"`
	MountedOn  string  `json:"mounted_on"`
	SizeKB     int64   `json:"size_kb"`
	UsedKB     int64   `json:"used_kb"`
	AvailKB    int64   `json:"avail_kb"`
	UsePercent float64 `json:"use_percent"`
}

// Network 网卡累计流量快照。
type Network struct {
	Name      string `json:"name"`
	RxBytes   int64  `json:"rx_bytes"`
	TxBytes   int64  `json:"tx_bytes"`
	RxPackets int64  `json:"rx_packets"`
	TxPackets int64  `json:"tx_packets"`
}

// Snapshot 一次采集结果。
type Snapshot struct {
	Hostname    string    `json:"hostname"`
	OS          string    `json:"os"`
	Kernel      string    `json:"kernel"`
	CPU         CPU       `json:"cpu"`
	Memory      Memory    `json:"memory"`
	Disks       []Disk    `json:"disks"`
	Network     []Network `json:"network"`
	CollectedAt string    `json:"collected_at"`
	// Warnings 记录采集过程中的降级信息（例如非 Linux 系统缺少 /proc）。
	Warnings []string `json:"warnings,omitempty"`
}

// collectScript 只读采集脚本，对缺失文件做了容错。
const collectScript = `echo '@@HOST'; (hostname 2>/dev/null || echo '-')
echo '@@OS'; (grep -E '^PRETTY_NAME=' /etc/os-release 2>/dev/null | head -n1 | cut -d= -f2- | tr -d '"' || echo '-')
echo '@@KERNEL'; (uname -sr 2>/dev/null || echo '-')
echo '@@UPTIME'; (cut -d' ' -f1 /proc/uptime 2>/dev/null || echo 0)
echo '@@LOAD'; (cat /proc/loadavg 2>/dev/null || echo '-')
echo '@@NCPU'; (getconf _NPROCESSORS_ONLN 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null || echo 0)
echo '@@CPU1'; (grep '^cpu ' /proc/stat 2>/dev/null || echo '-')
(sleep 0.5 2>/dev/null) || sleep 1
echo '@@CPU2'; (grep '^cpu ' /proc/stat 2>/dev/null || echo '-')
echo '@@MEM'; (cat /proc/meminfo 2>/dev/null || echo '-')
echo '@@DISK'; (df -kP 2>/dev/null || echo '-')
echo '@@NET'; (cat /proc/net/dev 2>/dev/null || echo '-')
echo '@@END'
`

// Collect 建立一次性 SSH 会话并采集快照。
func Collect(client *gossh.Client) (*Snapshot, error) {
	if client == nil {
		return nil, apperr.ErrBadRequest.WithMessage("连接不可用")
	}
	sess, err := client.NewSession()
	if err != nil {
		return nil, apperr.New(codeSessionFailed, "创建采集会话失败："+err.Error(), 502)
	}
	defer sess.Close()

	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(collectScript)
		ch <- result{out: out, err: err}
	}()

	select {
	case r := <-ch:
		if len(r.out) == 0 {
			if r.err != nil {
				return nil, apperr.New(codeCollectFailed, "采集命令执行失败："+r.err.Error(), 502)
			}
			return nil, apperr.New(codeCollectFailed, "未获取到任何监控数据", 502)
		}
		return parse(string(r.out)), nil
	case <-time.After(CollectTimeout):
		_ = sess.Signal(gossh.SIGKILL)
		return nil, apperr.New(codeTimeout, "监控采集超时", 504)
	}
}

func parse(raw string) *Snapshot {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	sections := map[string][]string{}
	current := ""
	seen := map[string]bool{}
	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "@@"))
			// @@END 之后的内容忽略，@@ 段名重复时只保留第一次（脚本本身会重复出现 @@NCPU 等情况除外）。
			if name == "END" {
				current = ""
				continue
			}
			// 允许同一段名重复出现（例如 CPU1/CPU2），这里用序号后缀区分。
			if seen[name] {
				name = name + "_dup"
			}
			seen[name] = true
			current = name
			sections[current] = nil
			continue
		}
		if current != "" {
			sections[current] = append(sections[current], strings.TrimSpace(line))
		}
	}

	snap := &Snapshot{CollectedAt: time.Now().UTC().Format(time.RFC3339)}
	snap.Hostname = firstValue(sections["HOST"])
	snap.OS = firstValue(sections["OS"])
	snap.Kernel = firstValue(sections["KERNEL"])

	if v, err := strconv.ParseFloat(firstValue(sections["UPTIME"]), 64); err == nil {
		snap.CPU.UptimeSecond = v
	}
	if n, err := strconv.Atoi(firstValue(sections["NCPU"])); err == nil {
		snap.CPU.Cores = n
	}
	loads := strings.Fields(firstValue(sections["LOAD"]))
	if len(loads) >= 3 {
		snap.CPU.Load1, _ = strconv.ParseFloat(loads[0], 64)
		snap.CPU.Load5, _ = strconv.ParseFloat(loads[1], 64)
		snap.CPU.Load15, _ = strconv.ParseFloat(loads[2], 64)
	}

	cpu1 := firstValue(sections["CPU1"])
	cpu2 := firstValue(sections["CPU2"])
	if usage, ok := cpuUsage(cpu1, cpu2); ok {
		snap.CPU.UsagePercent = usage
	} else {
		snap.Warnings = append(snap.Warnings, "/proc/stat 不可用，无法计算 CPU 使用率")
	}
	if snap.CPU.Cores == 0 {
		snap.CPU.Cores = 1
	}

	snap.Memory = parseMemory(sections["MEM"])
	if snap.Memory.TotalKB == 0 {
		snap.Warnings = append(snap.Warnings, "/proc/meminfo 不可用，无法读取内存信息")
	}

	snap.Disks = parseDisks(sections["DISK"])
	snap.Network = parseNetwork(sections["NET"])
	return snap
}

func firstValue(lines []string) string {
	for _, l := range lines {
		if l != "" {
			return l
		}
	}
	return ""
}

// cpuUsage 由两次 /proc/stat 采样计算 CPU 使用率。
func cpuUsage(line1, line2 string) (float64, bool) {
	f1 := strings.Fields(line1)
	f2 := strings.Fields(line2)
	if len(f1) < 5 || len(f2) < 5 || f1[0] != "cpu" || f2[0] != "cpu" {
		return 0, false
	}
	sum := func(fields []string) (total, idle float64, ok bool) {
		var values []float64
		for _, raw := range fields[1:] {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return 0, 0, false
			}
			values = append(values, v)
		}
		if len(values) < 4 {
			return 0, 0, false
		}
		for _, v := range values {
			total += v
		}
		idle = values[3]
		if len(values) > 4 {
			idle += values[4]
		}
		return total, idle, true
	}

	t1, i1, ok1 := sum(f1)
	t2, i2, ok2 := sum(f2)
	if !ok1 || !ok2 || t2 <= t1 {
		return 0, false
	}
	usage := (1 - (i2-i1)/(t2-t1)) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return round2(usage), true
}

func parseMemory(lines []string) Memory {
	var m Memory
	var swapFreeKB int64
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "MemTotal":
			m.TotalKB = value
		case "MemFree":
			m.FreeKB = value
		case "MemAvailable":
			m.AvailableKB = value
		case "SwapTotal":
			m.SwapTotalKB = value
		case "SwapFree":
			swapFreeKB = value
		}
	}
	if m.TotalKB > 0 {
		if m.AvailableKB == 0 {
			m.AvailableKB = m.FreeKB
		}
		m.UsedKB = m.TotalKB - m.AvailableKB
		if m.UsedKB < 0 {
			m.UsedKB = 0
		}
		m.UsedPercent = round2(float64(m.UsedKB) / float64(m.TotalKB) * 100)
	}
	if m.SwapTotalKB > 0 {
		m.SwapUsedKB = m.SwapTotalKB - swapFreeKB
		if m.SwapUsedKB < 0 {
			m.SwapUsedKB = 0
		}
	}
	return m
}

func parseDisks(lines []string) []Disk {
	out := make([]Disk, 0, 4)
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		if fields[0] == "Filesystem" {
			continue
		}
		// 过滤明显的伪文件系统，避免噪音。
		switch {
		case strings.HasPrefix(fields[0], "tmpfs"),
			strings.HasPrefix(fields[0], "devtmpfs"),
			strings.HasPrefix(fields[0], "udev"),
			strings.HasPrefix(fields[0], "shm"):
			continue
		}
		size, err1 := strconv.ParseInt(fields[1], 10, 64)
		used, err2 := strconv.ParseInt(fields[2], 10, 64)
		avail, err3 := strconv.ParseInt(fields[3], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		d := Disk{
			Filesystem: fields[0],
			SizeKB:     size,
			UsedKB:     used,
			AvailKB:    avail,
			MountedOn:  fields[len(fields)-1],
		}
		if size > 0 {
			d.UsePercent = round2(float64(used) / float64(size) * 100)
		}
		out = append(out, d)
	}
	return out
}

func parseNetwork(lines []string) []Network {
	out := make([]Network, 0, 4)
	for _, line := range lines {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" || name == "lo" || strings.Contains(name, "|") {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		rxBytes, err1 := strconv.ParseInt(fields[0], 10, 64)
		rxPackets, err2 := strconv.ParseInt(fields[1], 10, 64)
		txBytes, err3 := strconv.ParseInt(fields[8], 10, 64)
		txPackets, err4 := strconv.ParseInt(fields[9], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		out = append(out, Network{
			Name:      name,
			RxBytes:   rxBytes,
			TxBytes:   txBytes,
			RxPackets: rxPackets,
			TxPackets: txPackets,
		})
	}
	return out
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// ErrUnsupported 远端不支持采集。
var ErrUnsupported = errors.New("远端系统不支持监控采集")
