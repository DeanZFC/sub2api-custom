package ovhipv6

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StorageStats contains independent observations, not an atomic snapshot of the
// allocator and the database. Issued is a decimal high-water mark, never a
// count of retired addresses: live and failed allocations are also below it.
type StorageStats struct {
	SampledAt        time.Time       `json:"sampled_at"`
	Issued           *string         `json:"issued"`
	Prefix           string          `json:"prefix"`
	ControlAvailable bool            `json:"control_available"`
	ControlError     string          `json:"control_error,omitempty"`
	Files            StorageFiles    `json:"files"`
	Database         StorageDatabase `json:"database"`
}

type StorageFiles struct {
	Configured       bool          `json:"configured"`
	Available        bool          `json:"available"`
	Error            string        `json:"error,omitempty"`
	Items            []StorageFile `json:"items"`
	CounterBytes     *int64        `json:"counter_bytes"`
	CounterDiskBytes *int64        `json:"counter_disk_bytes"`
	TotalBytes       *int64        `json:"total_bytes"`
	TotalDiskBytes   *int64        `json:"total_disk_bytes"`
}

type StorageFile struct {
	Name      string `json:"name"`
	Bytes     *int64 `json:"bytes"`
	DiskBytes *int64 `json:"disk_bytes"`
	Error     string `json:"error,omitempty"`
}

// Database size includes only the dedicated live/unfinished lease relation and
// its indexes. Shared proxy/account tables and database logs are not included.
type StorageDatabase struct {
	Available  bool   `json:"available"`
	Error      string `json:"error,omitempty"`
	Rows       *int64 `json:"rows"`
	Reserved   *int64 `json:"reserved"`
	Bound      *int64 `json:"bound"`
	Retiring   *int64 `json:"retiring"`
	TableBytes *int64 `json:"table_bytes"`
	IndexBytes *int64 `json:"index_bytes"`
	TotalBytes *int64 `json:"total_bytes"`
}

// ReadStorageFiles observes metadata for three fixed regular files only. It
// neither opens file contents nor lists the directory, so a read-only mount
// with directory traversal permission suffices; credentials remain unreadable.
// Mount the directory, not individual files: the agent replaces their inodes.
func ReadStorageFiles(dir string) StorageFiles {
	out := StorageFiles{Items: []StorageFile{{Name: "counter.json"}, {Name: "counter.guard.json"}, {Name: "leases.json"}}}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		out.Error = "尚未配置 IPv6 状态目录的只读统计挂载"
		return out
	}
	out.Configured = true
	if !filepath.IsAbs(dir) {
		out.Error = "IPv6 状态统计目录必须是绝对路径"
		return out
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		out.Error = "IPv6 状态统计目录不可访问或不是普通目录"
		return out
	}
	out.Available = true
	for i := range out.Items {
		item := &out.Items[i]
		info, err := os.Lstat(filepath.Join(dir, item.Name))
		if err != nil || !info.Mode().IsRegular() {
			item.Error = "文件不存在、不可访问或不是普通文件"
			out.Available = false
			continue
		}
		size := info.Size()
		item.Bytes, item.DiskBytes = &size, storageDiskBytes(info)
	}
	if !out.Available {
		out.Error = "部分状态文件无法统计，缺失值不代表零占用"
	}
	out.CounterBytes = storageByteSum(out.Items[:2], false)
	out.CounterDiskBytes = storageByteSum(out.Items[:2], true)
	out.TotalBytes = storageByteSum(out.Items, false)
	out.TotalDiskBytes = storageByteSum(out.Items, true)
	return out
}

func storageByteSum(items []StorageFile, disk bool) *int64 {
	var total int64
	for _, item := range items {
		value := item.Bytes
		if disk {
			value = item.DiskBytes
		}
		if value == nil || *value < 0 || *value > int64(^uint64(0)>>1)-total {
			return nil
		}
		total += *value
	}
	return &total
}
