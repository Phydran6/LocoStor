// Package demo provides fake system backends so the UI can be explored on
// any machine (including Windows/macOS) without Samba, Ganesha or disks.
package demo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Phydran6/LocoStor/internal/fsutil"
	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/sysinfo"
)

// Username and Password log in to demo mode.
const Username = "admin"

// Password is the login password in demo mode.
const Password = "demo"

// Runner fakes the external commands LocoStor calls.
type Runner struct {
	mu    sync.Mutex
	users map[string]string // name -> uid
}

// NewRunner creates a Runner with a few Samba users.
func NewRunner() *Runner {
	return &Runner{users: map[string]string{"alice": "1000", "bob": "1001", "media": "1002"}}
}

// Run implements sysexec.Runner.
func (r *Runner) Run(_ context.Context, _ string, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch name {
	case "systemctl":
		if len(args) > 0 && args[0] == "is-active" {
			return []byte("active\n"), nil
		}
		return nil, nil
	case "pdbedit":
		names := make([]string, 0, len(r.users))
		for n := range r.users {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, n := range names {
			fmt.Fprintf(&b, "%s:%s:\n", n, r.users[n])
		}
		return []byte(b.String()), nil
	case "smbpasswd":
		user := args[len(args)-1]
		switch args[0] {
		case "-a":
			r.users[user] = fmt.Sprint(1000 + len(r.users))
		case "-x":
			delete(r.users, user)
		}
		return nil, nil
	case "smartctl":
		return smartctl(args), nil
	}
	// testparm, smbcontrol, id, useradd, ...: pretend success.
	return nil, nil
}

func smartctl(args []string) []byte {
	if len(args) > 0 && args[0] == "--scan-open" {
		return []byte(`{"devices":[
			{"name":"/dev/sda","type":"sat"},
			{"name":"/dev/sdb","type":"sat"},
			{"name":"/dev/sdc","type":"sat"},
			{"name":"/dev/sdd","type":"sat"},
			{"name":"/dev/nvme0","type":"nvme"}]}`)
	}
	dev := args[len(args)-1]
	switch dev {
	case "/dev/sda", "/dev/sdb":
		serial := map[string]string{"/dev/sda": "DEMO-A1", "/dev/sdb": "DEMO-B2"}[dev]
		return []byte(`{"smartctl":{"exit_status":0,"messages":[]},
			"device":{"name":"` + dev + `","type":"sat","protocol":"ATA"},
			"model_name":"WDC WD40EFRX-68N32N0","serial_number":"` + serial + `","firmware_version":"82.00A82",
			"user_capacity":{"bytes":4000787030016},"rotation_rate":5400,
			"smart_status":{"passed":true},"temperature":{"current":33},
			"power_on_time":{"hours":21873},"power_cycle_count":61,
			"ata_smart_attributes":{"table":[
				{"id":1,"name":"Raw_Read_Error_Rate","value":200,"worst":200,"thresh":51,"when_failed":"","raw":{"value":0,"string":"0"}},
				{"id":5,"name":"Reallocated_Sector_Ct","value":200,"worst":200,"thresh":140,"when_failed":"","raw":{"value":0,"string":"0"}},
				{"id":9,"name":"Power_On_Hours","value":71,"worst":71,"thresh":0,"when_failed":"","raw":{"value":21873,"string":"21873"}},
				{"id":194,"name":"Temperature_Celsius","value":117,"worst":102,"thresh":0,"when_failed":"","raw":{"value":33,"string":"33"}},
				{"id":197,"name":"Current_Pending_Sector","value":200,"worst":200,"thresh":0,"when_failed":"","raw":{"value":0,"string":"0"}},
				{"id":198,"name":"Offline_Uncorrectable","value":100,"worst":253,"thresh":0,"when_failed":"","raw":{"value":0,"string":"0"}}]}}`)
	case "/dev/sdc":
		return []byte(`{"smartctl":{"exit_status":64,"messages":[]},
			"device":{"name":"/dev/sdc","type":"sat","protocol":"ATA"},
			"model_name":"ST4000VN008-2DR166 (USB)","serial_number":"DEMO-C3","firmware_version":"SC60",
			"user_capacity":{"bytes":4000787030016},"rotation_rate":5980,
			"smart_status":{"passed":true},"temperature":{"current":41},
			"power_on_time":{"hours":35120},"power_cycle_count":402,
			"ata_smart_attributes":{"table":[
				{"id":5,"name":"Reallocated_Sector_Ct","value":98,"worst":98,"thresh":10,"when_failed":"","raw":{"value":24,"string":"24"}},
				{"id":9,"name":"Power_On_Hours","value":60,"worst":60,"thresh":0,"when_failed":"","raw":{"value":35120,"string":"35120"}},
				{"id":194,"name":"Temperature_Celsius","value":41,"worst":52,"thresh":0,"when_failed":"","raw":{"value":41,"string":"41 (Min/Max 18/52)"}},
				{"id":197,"name":"Current_Pending_Sector","value":100,"worst":100,"thresh":0,"when_failed":"","raw":{"value":8,"string":"8"}}]}}`)
	case "/dev/sdd":
		return []byte(`{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)","severity":"information"}]},
			"device":{"name":"/dev/sdd","type":"sat","protocol":"ATA"}}`)
	default:
		return []byte(`{"smartctl":{"exit_status":0,"messages":[]},
			"device":{"name":"/dev/nvme0","type":"nvme","protocol":"NVMe"},
			"model_name":"Samsung SSD 980 PRO 1TB","serial_number":"DEMO-N1","firmware_version":"5B2QGXA7",
			"nvme_total_capacity":1000204886016,"smart_status":{"passed":true},"temperature":{"current":45},
			"power_on_time":{"hours":8123},"power_cycle_count":210,
			"nvme_smart_health_information_log":{"percentage_used":3,"media_errors":0}}`)
	}
}

const mdstat = `Personalities : [raid1] [raid6] [raid5] [raid4]
md0 : active raid1 sdb[1] sda[0]
      3906886464 blocks super 1.2 [2/2] [UU]
      bitmap: 0/30 pages [0KB], 65536KB chunk

md1 : active raid5 sde[3] sdd[1] sdc[0]
      7813772288 blocks super 1.2 level 5, 512k chunk, algorithm 2 [3/2] [UU_]
      [=====>...............]  recovery = 27.4% (1070413824/3906886144) finish=312.6min speed=151226K/sec

unused devices: <none>
`

const smbConf = `[global]
   workgroup = WORKGROUP
   server string = %h server
   map to guest = bad user

[homes]
   comment = Home Directories
   browseable = no
   read only = yes

[Scans]
   comment = Scanner inbox
   path = /mnt/raid/scans
   writable = yes
   guest ok = yes
   force user = nobody
   create mask = 0664
`

const ganeshaConf = `NFS_CORE_PARAM {
    Protocols = 4;
}

EXPORT {
    Export_Id = 1;
    Path = "/mnt/raid/proxmox";
    Pseudo = "/proxmox";
    Access_Type = RW;
    Squash = No_Root_Squash;
    CLIENT {
        Clients = 192.168.1.2, 192.168.1.3;
        Access_Type = RW;
    }
    FSAL {
        Name = VFS;
    }
}

EXPORT {
    Export_Id = 2;
    Path = "/mnt/raid/iso";
    Pseudo = "/iso";
    Access_Type = RO;
    Attr_Expiration_Time = 60;
    FSAL {
        Name = VFS;
    }
}
`

// Env is a prepared demo environment.
type Env struct {
	Dir     string
	ProcDir string
	SysDir  string
	SMB     smb.Options
	NFS     nfs.Options
	// Files of the simulated Proxmox host.
	HostSMBConf string
	HostExports string
}

const hostSMBConf = `#======================= Global Settings =======================
[global]
   workgroup = WORKGROUP
   server string = %h server (Samba, Proxmox)
   map to guest = bad user

# Main data share on the RAID
[nas2]
   path = /mnt/raid/data
   writable = yes
   valid users = alice bob
   create mask = 0660
   directory mask = 0770

# Media for the TV
[media]
   path = /mnt/raid/media
   guest ok = yes
   read only = yes
`

const hostExports = `# /etc/exports: the access control list for filesystems which may be exported
#               to NFS clients.  See exports(5).
/mnt/raid/proxmox 192.168.1.2(rw,sync,no_subtree_check,no_root_squash) 192.168.1.3(rw,sync,no_subtree_check,no_root_squash)
/mnt/raid/iso *(ro,sync,no_subtree_check)
`

// Setup creates a temp directory with fake config files and seed data.
func Setup() (*Env, error) {
	dir, err := os.MkdirTemp("", "locostor-demo-")
	if err != nil {
		return nil, err
	}
	e := &Env{
		Dir:     dir,
		ProcDir: filepath.Join(dir, "proc"),
		SysDir:  filepath.Join(dir, "sys"),
		SMB: smb.Options{
			StatePath:     filepath.Join(dir, "smb-shares.json"),
			IncludePath:   filepath.Join(dir, "samba", "locostor.conf"),
			MainConf:      filepath.Join(dir, "samba", "smb.conf"),
			SkipPathCheck: true,
		},
		NFS: nfs.Options{
			StatePath:     filepath.Join(dir, "nfs-exports.json"),
			IncludePath:   filepath.Join(dir, "ganesha", "locostor.conf"),
			MainConf:      filepath.Join(dir, "ganesha", "ganesha.conf"),
			ExportsPath:   filepath.Join(dir, "exports"),
			SkipPathCheck: true,
		},
		HostSMBConf: filepath.Join(dir, "host", "samba", "smb.conf"),
		HostExports: filepath.Join(dir, "host", "exports"),
	}
	// Hand-written configs, as found on a system set up before LocoStor.
	existing := map[string]string{
		e.HostSMBConf:  hostSMBConf,
		e.HostExports:  hostExports,
		e.SMB.MainConf: smbConf,
		e.NFS.MainConf: ganeshaConf,
		e.NFS.ExportsPath: "# /etc/exports: the access control list for filesystems\n" +
			"/mnt/raid/backup 192.168.1.10(rw,sync,no_subtree_check,no_root_squash)\n",
	}
	for path, content := range existing {
		if err := fsutil.WriteFileAtomic(path, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(e.ProcDir, "mdstat"), []byte(mdstat), 0o644); err != nil {
		return nil, err
	}
	files := map[string]string{
		"sys/kernel/osrelease": "6.8.12-4-pve",
		"uptime":               "1234567.89 2345678.90",
		"loadavg":              "0.42 0.37 0.31 1/234 5678",
		"meminfo":              "MemTotal:        4194304 kB\nMemAvailable:    3145728 kB\n",
	}
	for name, content := range files {
		if err := fsutil.WriteFileAtomic(filepath.Join(e.ProcDir, name), []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	shares := []smb.Share{
		{Name: "Data", Path: "/mnt/raid/data", Comment: "Shared data", Browseable: true, ValidUsers: []string{"alice", "bob"}, Enabled: true},
		{Name: "Media", Path: "/mnt/raid/media", Comment: "Movies and music", ReadOnly: true, Browseable: true, GuestOK: true, ValidUsers: []string{}, Enabled: true},
		{Name: "Backup", Path: "/mnt/raid/backup", Browseable: false, ValidUsers: []string{"@backup"}, Enabled: false},
	}
	if err := fsutil.WriteJSON(e.SMB.StatePath, shares, 0o600); err != nil {
		return nil, err
	}
	exports := []nfs.Export{
		{ID: 100, Path: "/mnt/raid/data", Pseudo: "/data", Clients: []string{"192.168.1.0/24"}, Access: "RW", Squash: "root_squash", Protocols: []int{4}, Enabled: true},
		{ID: 101, Path: "/mnt/raid/media", Pseudo: "/media", Clients: []string{}, Access: "RO", Squash: "all_squash", Protocols: []int{3, 4}, Comment: "Media for players", Enabled: true},
	}
	if err := fsutil.WriteJSON(e.NFS.StatePath, exports, 0o600); err != nil {
		return nil, err
	}
	return e, nil
}

// Filesystems returns fake mounts for the dashboard.
func Filesystems() []sysinfo.Filesystem {
	const tb = int64(1) << 40
	const gb = int64(1) << 30
	return []sysinfo.Filesystem{
		{Mount: "/", Source: "/dev/mapper/pve-vm--105--disk--0", Type: "ext4", Size: 8 * gb, Used: 2 * gb, Avail: 6 * gb},
		{Mount: "/mnt/raid", Source: "/dev/md0", Type: "ext4", Size: 3*tb + 600*gb, Used: 2*tb + 300*gb, Avail: 1*tb + 300*gb},
		{Mount: "/mnt/raid5", Source: "/dev/md1", Type: "xfs", Size: 7*tb + 200*gb, Used: 1*tb + 800*gb, Avail: 5*tb + 400*gb},
	}
}
