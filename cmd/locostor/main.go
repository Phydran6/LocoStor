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
	"net"
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
	"github.com/Phydran6/LocoStor/internal/tlsutil"
	"github.com/Phydran6/LocoStor/internal/update"
	"github.com/Phydran6/LocoStor/web"
)

// version is set at build time via -ldflags "-X main.version=1.2.3".
var version = "dev"

const usageText = `LocoStor %s - storage management web UI

Usage:
  locostor [-config FILE] [-listen ADDR] [-demo]   start the web server
  locostor passwd [-user NAME]                     set the admin password (and username)
  locostor mfa-reset                               turn off two-factor login
  locostor tls self-signed [-listen :443]          serve HTTPS with a new self-signed certificate
  locostor tls files CERT KEY [-listen :443]       serve HTTPS with your own certificate
  locostor tls off [-listen :8080]                 serve plain HTTP (e.g. behind a reverse proxy)
  locostor version                                 print the version

All commands accept -config FILE (default /etc/locostor/config.json).
`

func main() {
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "":
		err = cmdServe(args)
	case "version":
		fmt.Println(version)
	case "passwd":
		err = cmdPasswd(args)
	case "mfa-reset":
		err = cmdMFAReset(args)
	case "tls":
		err = cmdTLS(args)
	default:
		fmt.Fprintf(os.Stderr, usageText, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, usageText, version) }
	return fs, fs.String("config", config.DefaultPath, "path to the config file")
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

func cmdPasswd(args []string) error {
	fs, configPath := newFlags("passwd")
	user := fs.String("user", "", "also set the admin username")
	fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *user != "" {
		if err := auth.ValidateUsername(*user); err != nil {
			return err
		}
	}
	pw, err := readPassword("New admin password: ")
	if err != nil {
		return err
	}
	if err := auth.ValidatePassword(pw); err != nil {
		return err
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
	if err := cfg.Update(func(c *config.Config) {
		c.PasswordHash = hash
		if *user != "" {
			c.Username = *user
		}
	}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved. Username: %s\n", cfg.Credentials().Username)
	return nil
}

func cmdMFAReset(args []string) error {
	fs, configPath := newFlags("mfa-reset")
	fs.Parse(args)
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.Update(func(c *config.Config) { c.TOTPSecret, c.RecoveryCodes = "", nil }); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Two-factor login is off. Restart LocoStor: systemctl restart locostor")
	return nil
}

func cmdTLS(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: locostor tls self-signed|files CERT KEY|off")
	}
	mode, args := args[0], args[1:]
	var files []string
	if mode == "files" {
		if len(args) < 2 {
			return errors.New("usage: locostor tls files CERT KEY")
		}
		files, args = args[:2], args[2:]
	}
	fs, configPath := newFlags("tls")
	defListen := ":443"
	if mode == "off" {
		defListen = ":8080"
	}
	listen := fs.String("listen", defListen, "listen address")
	redirect := fs.String("redirect", ":80,:8080", "plain HTTP addresses that redirect to HTTPS (empty = none)")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	var redirects []string
	for _, r := range strings.Split(*redirect, ",") {
		if r = strings.TrimSpace(r); r != "" && r != *listen {
			redirects = append(redirects, r)
		}
	}
	dir := filepath.Dir(*configPath)
	switch mode {
	case "self-signed":
		cert, key := filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
		if err := tlsutil.SelfSigned(cert, key, tlsutil.Hosts()); err != nil {
			return err
		}
		files = []string{cert, key}
	case "files":
		for i, f := range files {
			abs, err := filepath.Abs(f)
			if err != nil {
				return err
			}
			files[i] = abs
		}
		if err := tlsutil.Check(files[0], files[1]); err != nil {
			return fmt.Errorf("certificate files are not usable: %w", err)
		}
	case "off":
		return save(cfg, *listen, "", "", nil)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	return save(cfg, *listen, files[0], files[1], redirects)
}

func save(cfg *config.Config, listen, cert, key string, redirects []string) error {
	if err := cfg.Update(func(c *config.Config) {
		c.Listen, c.TLSCert, c.TLSKey, c.HTTPRedirect = listen, cert, key, redirects
	}); err != nil {
		return err
	}
	scheme := "http"
	if cert != "" {
		scheme = "https"
	}
	fmt.Fprintf(os.Stderr, "Saved: %s on %s. Restart LocoStor: systemctl restart locostor\n", scheme, listen)
	return nil
}

func cmdServe(args []string) error {
	fs, configPath := newFlags("locostor")
	listen := fs.String("listen", "", "listen address, overrides the config (e.g. :8080)")
	demoMode := fs.Bool("demo", false, "run with fake data (no system changes)")
	fs.Parse(args)

	var (
		cfg         *config.Config
		run         sysexec.Runner = sysexec.OS{}
		smbOpts     smb.Options
		nfsOpts     nfs.Options
		procDir     = "/proc"
		sysDir      = "/sys"
		store       auth.Store
		sessionFile string
	)

	if *demoMode {
		env, err := demo.Setup()
		if err != nil {
			return err
		}
		defer os.RemoveAll(env.Dir)
		cfg = &config.Config{Listen: ":8080", UpdateRepo: "Phydran6/LocoStor"}
		if store, err = auth.NewMemStore(demo.Username, demo.Password); err != nil {
			return err
		}
		run = demo.NewRunner()
		smbOpts, nfsOpts = env.SMB, env.NFS
		procDir, sysDir = env.ProcDir, env.SysDir
		log.Printf("demo mode: log in as %q with password %q", demo.Username, demo.Password)
	} else {
		var err error
		if cfg, err = config.Load(*configPath); err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
			return err
		}
		if cfg.Credentials().PasswordHash == "" {
			if err := initialPassword(cfg); err != nil {
				return err
			}
		}
		store = cfg
		sessionFile = filepath.Join(cfg.DataDir, "sessions.json")
		smbOpts, nfsOpts = smb.DefaultOptions(cfg.DataDir), nfs.DefaultOptions(cfg.DataDir)
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	useTLS := cfg.TLSCert != "" && cfg.TLSKey != ""

	srv := &http.Server{
		Addr:              cfg.Listen,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      3 * time.Minute, // SMART refresh can take a while
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		TLSConfig:         tlsutil.ServerConfig(),
	}

	exe, _ := os.Executable()
	restart := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		reexec(exe)
	}

	updDisabled := ""
	if *demoMode {
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
	if *demoMode {
		sysInfo = func() sysinfo.Info {
			i := info.Read()
			i.OS = "Debian GNU/Linux 12 (bookworm)"
			i.Filesystems = demo.Filesystems()
			return i
		}
	}

	server := &api.Server{
		Version: version,
		Demo:    *demoMode,
		Repo:    cfg.UpdateRepo,
		Auth:    auth.New(store, sessionFile),
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

	if useTLS {
		startRedirects(cfg.HTTPRedirect, cfg.Listen)
	}

	errc := make(chan error, 1)
	go func() {
		if useTLS {
			log.Printf("LocoStor %s listening on %s (HTTPS)", version, cfg.Listen)
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			log.Printf("LocoStor %s listening on %s", version, cfg.Listen)
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

// startRedirects serves plain HTTP on addrs and redirects every request to
// the HTTPS listener, so old http:// bookmarks keep working.
func startRedirects(addrs []string, tlsListen string) {
	_, port, _ := net.SplitHostPort(tlsListen)
	suffix := ""
	if port != "" && port != "443" {
		suffix = ":" + port
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			host = hh
		}
		if strings.Contains(host, ":") { // IPv6 literal
			host = "[" + host + "]"
		}
		http.Redirect(w, r, "https://"+host+suffix+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
	for _, addr := range addrs {
		s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
		go func() {
			if err := s.ListenAndServe(); err != nil {
				log.Printf("HTTP redirect on %s not available: %v", addr, err)
			}
		}()
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
	if err := cfg.Update(func(c *config.Config) { c.PasswordHash = hash }); err != nil {
		return fmt.Errorf("save initial password: %w", err)
	}
	log.Printf("no admin password set - generated one for user %q: %s", cfg.Credentials().Username, pw)
	log.Printf("change it in the UI or with: %s passwd", filepath.Base(os.Args[0]))
	return nil
}
