package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/paths"
	"github.com/shirou/gopsutil/v3/disk"
)

func TestDiskCheckUsesOneMeasurementPerVolume(t *testing.T) {
	root := maintenanceTestDir(t)
	directories := paths.Directories{Data: root, Logs: filepath.Join(root, "logs"), Backup: filepath.Join(root, "backups")}
	for _, directory := range []string{directories.Logs, directories.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	volumes, err := collectDiskVolumes(directories)
	if err != nil {
		t.Fatalf("collectDiskVolumes() error: %v", err)
	}
	if len(volumes) != 1 || volumes[0].path != root {
		t.Fatalf("disk volumes = %+v; want one volume using data path %q", volumes, root)
	}
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.DiskLow.Enabled = false
	if err := checkDiskSpace(directories, configuration, log.New(io.Discard, "", 0), time.Now()); err != nil {
		t.Fatalf("disabled disk check error: %v", err)
	}
	sendDiskLowEmail(configuration, root, 90, 10, 85)
}

func TestDiskCheckDeduplicatesAlertsAndClearsRecoveredVolumes(t *testing.T) {
	root := maintenanceTestDir(t)
	directories := paths.Directories{Data: root, Logs: filepath.Join(root, "logs"), Backup: filepath.Join(root, "backups")}
	for _, directory := range []string{directories.Logs, directories.Backup} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	volumes, err := collectDiskVolumes(directories)
	if err != nil || len(volumes) != 1 {
		t.Fatalf("collectDiskVolumes() = %+v, %v", volumes, err)
	}
	previous := diskAlerts.volumes
	diskAlerts.Lock()
	diskAlerts.volumes = make(map[string]diskAlertState)
	diskAlerts.Unlock()
	t.Cleanup(func() {
		diskAlerts.Lock()
		diskAlerts.volumes = previous
		diskAlerts.Unlock()
	})
	configuration := config.DefaultConfig()
	configuration.Server.Notifications.DiskLow.Enabled = true
	configuration.Server.Notifications.Email.Enabled = false
	configuration.Server.Notifications.DiskLow.ThresholdPercent = int(volumes[0].usage.UsedPercent)
	if configuration.Server.Notifications.DiskLow.ThresholdPercent < 1 {
		configuration.Server.Notifications.DiskLow.ThresholdPercent = 1
	}
	configuration.Server.Notifications.DiskLow.Repeat = "invalid"
	logger := log.New(io.Discard, "", 0)
	now := time.Now()
	if err := checkDiskSpace(directories, configuration, logger, now); err != nil {
		t.Fatalf("initial disk check error: %v", err)
	}
	state := diskAlerts.volumes[volumes[0].key]
	if volumes[0].usage.UsedPercent >= 1 && (!state.active || !state.lastAlert.Equal(now)) {
		t.Fatalf("initial alert state = %+v", state)
	}
	if volumes[0].usage.UsedPercent >= 1 {
		if err := checkDiskSpace(directories, configuration, logger, now.Add(time.Minute)); err != nil {
			t.Fatalf("repeated disk check error: %v", err)
		}
		state = diskAlerts.volumes[volumes[0].key]
		if !state.lastAlert.Equal(now) {
			t.Fatalf("repeated alert changed its timestamp: %+v", state)
		}
	}
	if volumes[0].usage.UsedPercent < 95 {
		configuration.Server.Notifications.DiskLow.ThresholdPercent = 100
		if err := checkDiskSpace(directories, configuration, logger, now.Add(2*time.Hour)); err != nil {
			t.Fatalf("recovery disk check error: %v", err)
		}
		if _, exists := diskAlerts.volumes[volumes[0].key]; exists {
			t.Fatal("recovered volume remained in the alert state")
		}
	}
}

func TestCollectDiskVolumesIgnoresEmptyDirectories(t *testing.T) {
	volumes, err := collectDiskVolumes(paths.Directories{})
	if err != nil || len(volumes) != 0 {
		t.Fatalf("empty disk directories = %+v, %v; want none", volumes, err)
	}
}

func TestDiskVolumeKeyUsesDeviceAndMostSpecificMount(t *testing.T) {
	partitions := []disk.PartitionStat{
		{Device: "/dev/shared", Mountpoint: "/data"},
		{Device: "/dev/shared", Mountpoint: "/logs"},
		{Device: "/dev/separate", Mountpoint: "/data/archive"},
	}
	for path, want := range map[string]string{
		"/data/app":            "/dev/shared",
		"/logs/app":            "/dev/shared",
		"/data/archive/backup": "/dev/separate",
	} {
		if got := diskVolumeKey(path, &disk.UsageStat{Path: path}, partitions); got != want {
			t.Errorf("diskVolumeKey(%q) = %q, want %q", path, got, want)
		}
	}
}
