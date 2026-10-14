package daemon

import (
	"bytes"
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

// serviceEnv lists the variables a service gets copied from the installer.
var serviceEnv = []string{"XDG_CACHE_HOME", "XDG_DATA_HOME"}

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
	home, err := os.UserHomeDir()
	if err != nil {
		return service{}, fmt.Errorf("recycler: locating home directory: %w", err)
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
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, arg := range args {
		b.WriteString("\t\t<string>" + xmlText(arg) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	if env := s.copiedEnv(); len(env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, kv := range env {
			b.WriteString("\t\t<key>" + xmlText(kv[0]) + "</key>\n\t\t<string>" + xmlText(kv[1]) + "</string>\n")
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString(`	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + xmlText(s.log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlText(s.log) + `</string>
</dict>
</plist>
`)
	return b.Bytes(), nil
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
	var b bytes.Buffer
	b.WriteString("[Unit]\nDescription=recycler disk-pressure daemon\n\n[Service]\n")
	b.WriteString("ExecStart=" + strings.Join(quoted, " ") + "\n")
	for _, kv := range s.copiedEnv() {
		b.WriteString("Environment=" + systemdQuote(kv[0]+"="+kv[1]) + "\n")
	}
	b.WriteString("Restart=always\nRestartSec=1\n\n[Install]\nWantedBy=default.target\n")
	return b.Bytes(), nil
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
	user := xmlText(s.user)
	return `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>recycler disk-pressure daemon</Description>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <UserId>` + user + `</UserId>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <UserId>` + user + `</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>999</Count>
    </RestartOnFailure>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>` + xmlText(s.exe) + `</Command>
      <Arguments>daemon --standby</Arguments>
    </Exec>
  </Actions>
</Task>
`
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

// copiedEnv returns the serviceEnv variables the installer has set, in order.
func (s service) copiedEnv() [][2]string {
	var env [][2]string
	for _, name := range serviceEnv {
		if v := s.getenv(name); v != "" {
			env = append(env, [2]string{name, v})
		}
	}
	return env
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
