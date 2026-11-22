package agent

import (
	"context"
	"os"
	"probex/internal/protocol"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
)

func Collect(parent context.Context) protocol.Info {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	i := protocol.Info{OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), CollectedAt: time.Now().UTC(), Errors: map[string]string{}}
	if name, err := os.Hostname(); err == nil {
		i.Hostname = name
	} else {
		i.Errors["hostname"] = err.Error()
	}
	if h, err := host.InfoWithContext(ctx); err == nil {
		i.Platform = h.Platform + " " + h.PlatformVersion
		i.Kernel = h.KernelVersion
		i.Uptime = h.Uptime
	} else {
		i.Errors["host"] = err.Error()
	}
	if c, err := cpu.InfoWithContext(ctx); err == nil && len(c) > 0 {
		i.CPUModel = c[0].ModelName
	} else if err != nil {
		i.Errors["cpu"] = err.Error()
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		i.MemoryTotal = &m.Total
		i.MemoryAvailable = &m.Available
	} else {
		i.Errors["memory"] = err.Error()
	}
	root := "/"
	if runtime.GOOS == "windows" {
		root = os.Getenv("SystemDrive") + "\\"
	}
	if d, err := disk.UsageWithContext(ctx, root); err == nil {
		i.DiskTotal = &d.Total
		i.DiskFree = &d.Free
	} else {
		i.Errors["disk"] = err.Error()
	}
	if n, err := psnet.IOCountersWithContext(ctx, false); err == nil && len(n) > 0 {
		i.BytesSent = &n[0].BytesSent
		i.BytesRecv = &n[0].BytesRecv
	} else if err != nil {
		i.Errors["network"] = err.Error()
	}
	if p, err := cpu.PercentWithContext(ctx, 0, false); err == nil && len(p) > 0 {
		i.CPUPercent = &p[0]
	} else if err != nil {
		i.Errors["cpu_percent"] = err.Error()
	}
	return i
}
