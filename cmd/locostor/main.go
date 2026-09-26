// Command locostor is a small storage management web UI for Proxmox LXC
// containers.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/Phydran6/LocoStor/internal/api"
	"github.com/Phydran6/LocoStor/internal/auth"
	"github.com/Phydran6/LocoStor/internal/config"
	"github.com/Phydran6/LocoStor/internal/demo"
	"github.com/Phydran6/LocoStor/internal/nfs"
	"github.com/Phydran6/LocoStor/internal/raid"
	"github.com/Phydran6/LocoStor/internal/smart"
	"github.com/Phydran6/LocoStor/internal/smb"
	"github.com/Phydran6/LocoStor/internal/sysexec"
	"github.com/Phydran6/LocoStor/internal/sysinfo"
	"github.com/Phydran6/LocoStor/internal/update"
	"github.com/Phydran6/LocoStor/web"
)

// version is set at build time via -ldflags "-X main.version=1.2.3".
var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `LocoStor %s - storage management web UI

Usage:
  locostor [flags]          start the web server
  locostor passwd [flags]   set the admin password
  locostor version          print the version

Flags:
`, version)
	flag.PrintDefaults()
}

func main() {
	configPath := flag.String("config", config.DefaultPath, "path to the config file")
	listen := flag.String("listen", "", "listen address, overrides the config (e.g. :8080)")
	demoMode := flag.Bool("demo", false, "run with fake data (no system changes)")
	flag.Usage = usage

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	_ = flag.CommandLine.Parse(args)

	switch cmd {
	case "":
		if err := serve(*configPath, *listen, *demoMode); err != nil {
			log.Fatal(err)
		}
	case "version":
		fmt.Println(version)
	case "passwd":
		if err := passwd(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func passwd(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	pw, err := readPassword("New admin password: ")
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		again, err := readPassword("Repeat password: ")
		if err != nil {
			return err
		}
		if again != pw {
			return errors.New("passwords do not match")
		}
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := cfg.SetPasswordHash(hash); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Password saved to", configPath)
	return nil
}

func serve(configPath, listen string, demoMode bool) error {
	var (
		cfg     *config.Config
		run     sysexec.Runner = sysexec.OS{}
		smbOpts smb.Options
		nfsOpts nfs.Options
		procDir = "/proc"
		sysDir  = "/sys"
		pwStore auth.PasswordStore
	)

	if demoMode {
		env, err := demo.Setup()
		if err != nil {
			return err
		}
		defer os.RemoveAll(env.Dir)
		cfg = &config.Config{Listen: ":8080", UpdateRepo: "Phydran6/LocoStor"}
		hash, _ := auth.HashPassword(demo.Password)
		pwStore = &memStore{hash: hash}
		run = demo.NewRunner()
		smbOpts, nfsOpts = env.SMB, env.NFS
		procDir, sysDir = env.ProcDir, env.SysDir
		log.Printf("demo mode: login password is %q", demo.Password)
	} else {
		var err error
		if cfg, err = config.Load(configPath); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return err
		}
		if cfg.GetPasswordHash() == "" {
			if err := initialPassword(cfg); err != nil {
				return err
			}
		}
		pwStore = cfg
		smbOpts, nfsOpts = smb.DefaultOptions(cfg.DataDir), nfs.DefaultOptions(cfg.DataDir)
	}
	if listen != "" {
		cfg.Listen = listen
	}

	srv := &http.Server{Addr: cfg.Listen, ReadHeaderTimeout: 10 * time.Second}

	exe, _ := os.Executable()
	restart := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		reexec(exe)
	}

	updDisabled := ""
	if demoMode {
		updDisabled = "self-update is disabled in demo mode"
	}
	updater := update.New(update.Options{
		Repo:     cfg.UpdateRepo,
		Current:  version,
		Disabled: updDisabled,
		Restart:  restart,
		Run:      sysexec.OS{},
	})

	info := sysinfo.Reader{ProcRoot: procDir, OSRelease: "/etc/os-release"}
	sysInfo := info.Read
	if demoMode {
		sysInfo = func() sysinfo.Info {
			i := info.Read()
			i.OS = "Debian GNU/Linux 12 (bookworm)"
			i.Filesystems = demo.Filesystems()
			return i
		}
	}

	server := &api.Server{
		Version: version,
		Demo:    demoMode,
		Auth:    auth.New(pwStore),
		SMB:     smb.New(run, smbOpts),
		NFS:     nfs.New(run, nfsOpts),
		SMART:   smart.New(run, cfg.SmartDevices),
		Updater: updater,
		RAID:    func() ([]raid.Array, error) { return raid.Read(procDir, sysDir) },
		SysInfo: sysInfo,
		Web:     web.Dist(),
	}
	srv.Handler = server.Handler()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go updater.RunPeriodic(ctx, 6*time.Hour)

	errc := make(chan error, 1)
	go func() {
		log.Printf("LocoStor %s listening on %s", version, cfg.Listen)
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			// Shut down for a restart; reexec takes over from here.
			select {}
		}
		return err
	case <-ctx.Done():
		log.Print("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(sctx)
	}
}

// initialPassword generates a random admin password on first start and
// prints it to the log (journalctl -u locostor).
func initialPassword(cfg *config.Config) error {
	pw := auth.RandomToken(12)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	if err := cfg.SetPasswordHash(hash); err != nil {
		return fmt.Errorf("save initial password: %w", err)
	}
	log.Printf("no admin password set - generated initial password: %s", pw)
	log.Printf("change it in the UI or with: %s passwd", filepath.Base(os.Args[0]))
	return nil
}

// memStore keeps the password hash in memory (demo mode).
type memStore struct{ hash string }

func (m *memStore) GetPasswordHash() string        { return m.hash }
func (m *memStore) SetPasswordHash(h string) error { m.hash = h; return nil }
