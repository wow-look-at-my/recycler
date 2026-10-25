// serviceLabel names the daemon's job to launchd and its task to Windows.

package daemon

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/wow-look-at-my/recycler/internal/bin"
)

const (
	// serviceLabel names the daemon's job to launchd and its task to Windows.
	serviceLabel = "build.pazer.recycler"

	// systemdUnit names the daemon's systemd user unit.
	systemdUnit = "recycler.service"

	// unloadTimeout bounds the wait for launchd to finish unloading the job an install replaces.
	unloadTimeout = 10 * time.Second
)

// The embedded service files hold these where the value is only known at install time.
const (
	argToken   = "{{arg}}"
	argsToken  = "{{args}}"
	execToken  = "{{exec}}"
	exeToken   = "{{exe}}"
	labelToken = "{{label}}"
	logToken   = "{{log}}"
	userToken  = "{{user}}"
)

//go:embed launchd.plist
var launchdPlistFile string

//go:embed launchd_arg.plist
var launchdArgFile string

//go:embed scheduled_task.xml
var scheduledTaskFile string

//go:embed systemd.service
var systemdServiceFile string

// A service is what Install needs to register the daemon with a service manager.
type service struct {
	exe    string
	home   string
	log    string
	uid    int
	user   string
	getenv func(string) string
	run    func(name string, args ...string) error
}

// newService describes the daemon in exe for the user running this.
func newService(exe string) (service, error) {
	home, err := accountHome()
	if err != nil {
		return service{}, err
	}
	log, err := LogPath()
	if err != nil {
		return service{}, err
	}
	return service{exe: exe, home: home, log: log, uid: os.Getuid(), getenv: os.Getenv, run: runCommand}, nil
}

// install registers the daemon with the service manager of the kernel named, as
// uname -s spells it, and returns the file it wrote.
func (s service) install(kernel string) (string, error) {
	switch kernel {
	case "Darwin":
		return s.launchd()
	case "Linux":
		return s.systemd()
	case "Windows":
		return s.scheduledTask()
	}
	return "", fmt.Errorf("%w: recycler installs no service on %s", bin.ErrUnsupported, kernel)
}

// launchd writes a LaunchAgent, which starts at login and is restarted whenever it exits.
func (s service) launchd() (string, error) {
	dir := filepath.Join(s.home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, serviceLabel+".plist")
	plist, err := s.launchdPlist()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, plist, 0o644); err != nil {
		return "", err
	}

	domain := fmt.Sprintf("gui/%d", s.uid)
	target := domain + "/" + serviceLabel
	// A loaded job keeps the definition it was loaded with.
	if s.run("launchctl", "print", target) == nil {
		if err := s.run("launchctl", "bootout", target); err != nil {
			return "", err
		}
		if err := s.awaitUnload(target); err != nil {
			return "", err
		}
	}
	if err := s.run("launchctl", "enable", target); err != nil {
		return "", err
	}
	if err := s.run("launchctl", "bootstrap", domain, path); err != nil {
		return "", err
	}
	return path, nil
}

// awaitUnload returns once launchd no longer knows target. A bootout returns
// before the job has stopped, and bootstrapping over it fails.
func (s service) awaitUnload(target string) error {
	deadline := time.Now().Add(unloadTimeout)
	for s.run("launchctl", "print", target) == nil {
		if time.Now().After(deadline) {
			return fmt.Errorf("recycler: launchd still has %s loaded %s after bootout", target, unloadTimeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func (s service) launchdPlist() ([]byte, error) {
	args, err := daemonArgs(s.exe)
	if err != nil {
		return nil, err
	}
	var lines bytes.Buffer
	for _, arg := range args {
		lines.WriteString(strings.NewReplacer(argToken, xmlText(arg)).Replace(launchdArgFile))
	}
	return []byte(strings.NewReplacer(
		labelToken, serviceLabel,
		logToken, xmlText(s.log),
		argsToken, lines.String(),
	).Replace(launchdPlistFile)), nil
}

// systemd writes a user unit, which starts at login and is restarted whenever it exits.
func (s service) systemd() (string, error) {
	config := s.getenv("XDG_CONFIG_HOME")
	if config == "" || !filepath.IsAbs(config) {
		config = filepath.Join(s.home, ".config")
	}
	dir := filepath.Join(config, "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, systemdUnit)
	unit, err := s.systemdUnit()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, unit, 0o644); err != nil {
		return "", err
	}
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", systemdUnit},
		{"--user", "restart", systemdUnit},
	} {
		if err := s.run("systemctl", args...); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (s service) systemdUnit() ([]byte, error) {
	args, err := daemonArgs(s.exe)
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = systemdQuote(arg)
	}
	return []byte(strings.NewReplacer(execToken, strings.Join(quoted, " ")).Replace(systemdServiceFile)), nil
}

// systemdQuote spells s as one double-quoted word of a unit file, where % starts
// a specifier.
func systemdQuote(s string) string {
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s)
	return `"` + s + `"`
}

// scheduledTask registers a task that starts at logon and is restarted when it fails.
func (s service) scheduledTask() (string, error) {
	path, err := statePath("daemon-task.xml")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, utf16File(s.taskXML()), 0o600); err != nil {
		return "", err
	}
	if err := s.run("schtasks", "/Create", "/TN", serviceLabel, "/XML", path, "/F"); err != nil {
		return "", err
	}
	if err := s.run("schtasks", "/Run", "/TN", serviceLabel); err != nil {
		return "", err
	}
	return path, nil
}

func (s service) taskXML() string {
	return strings.NewReplacer(
		userToken, xmlText(s.user),
		exeToken, xmlText(s.exe),
	).Replace(scheduledTaskFile)
}

// utf16File encodes s as UTF-16LE with a byte order mark, the encoding
// schtasks reads a task definition in.
func utf16File(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 2, 2+2*len(units))
	out[0], out[1] = 0xff, 0xfe
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

// apeMagic opens every Actually Portable Executable.
var apeMagic = []string{"MZqFpD='", "jartsr='", "APEDBG='"}

// daemonArgs returns the command line that runs exe as a standby daemon, through
// /bin/sh when exe is an Actually Portable Executable.
func daemonArgs(exe string) ([]string, error) {
	f, err := os.Open(exe)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 8)
	if _, err := io.ReadFull(f, head); err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("recycler: reading %s: %w", exe, err)
	}
	args := []string{exe, "daemon", "--standby"}
	for _, magic := range apeMagic {
		if bytes.HasPrefix(head, []byte(magic)) {
			return append([]string{"/bin/sh"}, args...), nil
		}
	}
	return args, nil
}

func xmlText(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// runCommand runs a service manager command, and names its output when it fails.
func runCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("recycler: %s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}
