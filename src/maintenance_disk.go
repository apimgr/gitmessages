package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/shirou/gopsutil/v3/disk"
)

type diskAlertState struct {
	lastAlert time.Time
	active    bool
}

var diskAlerts = struct {
	sync.Mutex
	volumes map[string]diskAlertState
}{volumes: make(map[string]diskAlertState)}

type diskVolumeUsage struct {
	path  string
	key   string
	usage *disk.UsageStat
}

func collectDiskVolumes(directories paths.Directories) ([]diskVolumeUsage, error) {
	ordered := []string{directories.Data, directories.Logs, directories.Backup}
	volumes := make([]diskVolumeUsage, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	partitions, _ := disk.Partitions(true)
	for _, directory := range ordered {
		if strings.TrimSpace(directory) == "" {
			continue
		}
		path, err := filepath.Abs(directory)
		if err != nil {
			return nil, fmt.Errorf("resolve disk check path: %w", err)
		}
		usage, err := disk.Usage(path)
		if err != nil {
			return nil, fmt.Errorf("measure disk usage for %s: %w", path, err)
		}
		volume := diskVolumeKey(path, usage, partitions)
		if _, exists := seen[volume]; exists {
			continue
		}
		seen[volume] = struct{}{}
		volumes = append(volumes, diskVolumeUsage{path: path, key: volume, usage: usage})
	}
	return volumes, nil
}

func diskVolumeKey(path string, usage *disk.UsageStat, partitions []disk.PartitionStat) string {
	longestMount := -1
	key := ""
	for _, partition := range partitions {
		mountpoint := filepath.Clean(partition.Mountpoint)
		relative, err := filepath.Rel(mountpoint, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || len(mountpoint) <= longestMount {
			continue
		}
		longestMount = len(mountpoint)
		key = strings.TrimSpace(partition.Device)
		if key == "" {
			key = mountpoint
		}
	}
	if key != "" {
		return key
	}
	if usage.Path != "" {
		return usage.Path
	}
	return path
}

func checkDiskSpace(directories paths.Directories, configuration *config.Config, logger *log.Logger, now time.Time) error {
	policy := configuration.Server.Notifications.DiskLow
	if !policy.Enabled {
		return nil
	}
	threshold := policy.ThresholdPercent
	if threshold <= 0 || threshold > 100 {
		threshold = 85
	}
	repeat, err := time.ParseDuration(policy.Repeat)
	if err != nil || repeat <= 0 {
		repeat = 24 * time.Hour
	}
	volumes, err := collectDiskVolumes(directories)
	if err != nil {
		return err
	}
	for _, volume := range volumes {
		key := volume.key
		used := volume.usage.UsedPercent
		diskAlerts.Lock()
		state := diskAlerts.volumes[key]
		if used < float64(threshold-5) {
			if state.active {
				logger.Printf("INFO Disk space recovered path=%q used_percent=%.1f threshold_percent=%d", volume.path, used, threshold)
			}
			delete(diskAlerts.volumes, key)
			diskAlerts.Unlock()
			continue
		}
		if used < float64(threshold) || !state.lastAlert.IsZero() && now.Sub(state.lastAlert) < repeat {
			diskAlerts.Unlock()
			continue
		}
		state.active = true
		state.lastAlert = now
		diskAlerts.volumes[key] = state
		diskAlerts.Unlock()

		free := volume.usage.Free
		logger.Printf("WARN disk_low path=%q used_percent=%.1f free_bytes=%d threshold_percent=%d", volume.path, used, free, threshold)
		sendDiskLowEmail(configuration, volume.path, used, free, threshold)
	}
	return nil
}

func sendDiskLowEmail(configuration *config.Config, path string, used float64, free uint64, threshold int) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := sendOperatorEmail(ctx, configuration, "disk_low", "", map[string]string{
		"path": path, "used_percent": fmt.Sprintf("%.1f%%", used), "free": humanBytes(free),
		"threshold_percent": fmt.Sprintf("%d%%", threshold),
	}); err != nil {
		log.Printf("WARN disk_low.email_failed error=%q", err)
	}
}

func humanBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", amount, units[unit])
}
