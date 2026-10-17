package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/recycler/internal/bin"
)

// fakeManager stands in for launchctl, systemctl and schtasks, and records what
// it was asked to do.
type fakeManager struct {
	calls  []string
	loaded bool   // launchctl print succeeds while this holds
	refuse string // an argument that makes a command fail
}

func (f *fakeManager) run(name string, args ...string) error {
	f.calls = append(f.calls, strings.Join(append([]string{name}, args...), " "))
	if slices.Contains(args, f.refuse) {
		return errors.New("refused")
	}
	if name != "launchctl" {
		return nil
	}
	switch args[0] {
	case "print":
		if !f.loaded {
			return errors.New("not loaded")
		}
	case "bootout":
		f.loaded = false
	case "bootstrap":
		f.loaded = true
	}
	return nil
}

// writeExe writes an executable that starts with head.
func writeExe(t *testing.T, name, head string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(head+"\nrest of the program\n"), 0o755))
	return path
}

func testService(t *testing.T, exe string, env map[string]string, m *fakeManager) service {
	t.Helper()
	home := t.TempDir()
	return service{
		exe:    exe,
		home:   home,
		log:    filepath.Join(home, "daemon & log"),
		uid:    501,
		user:   `HOST\me`,
		getenv: func(name string) string { return env[name] },
		run:    m.run,
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// An Actually Portable Executable is a shell script to the kernel, so launchd
// has to run it through one.
func TestTheLaunchAgentRunsAPortableExecutableThroughAShell(t *testing.T) {
	exe := writeExe(t, "recycler", "MZqFpD='")
	m := &fakeManager{}
	s := testService(t, exe, map[string]string{"XDG_CACHE_HOME": "/cache", "XDG_DATA_HOME": "/data"}, m)

	path, err := s.install("Darwin")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(s.home, "Library", "LaunchAgents", serviceLabel+".plist"), path)

	plist := readFile(t, path)
	assert.Contains(t, plist, "<string>/bin/sh</string>\n\t\t<string>"+exe+"</string>\n\t\t<string>daemon</string>\n\t\t<string>--standby</string>")
	assert.Contains(t, plist, "<key>KeepAlive</key>\n\t<true/>")
	assert.Contains(t, plist, "<key>RunAtLoad</key>\n\t<true/>")
	assert.NotContains(t, plist, "EnvironmentVariables", "the installer's environment would give the daemon its lock and bin")
	assert.Contains(t, plist, "<string>"+filepath.Join(s.home, "daemon &amp; log")+"</string>")

	assert.Equal(t, []string{
		"launchctl print gui/501/" + serviceLabel,
		"launchctl enable gui/501/" + serviceLabel,
		"launchctl bootstrap gui/501 " + path,
	}, m.calls)
}

// Installing over a loaded job replaces it, because launchd keeps running the
// definition a job was loaded with.
func TestReinstallingTheLaunchAgentReplacesTheLoadedJob(t *testing.T) {
	exe := writeExe(t, "recycler", "\x7fELF")
	m := &fakeManager{loaded: true}
	s := testService(t, exe, nil, m)

	path, err := s.install("Darwin")
	require.NoError(t, err)
	assert.Equal(t, []string{
		"launchctl print gui/501/" + serviceLabel,
		"launchctl bootout gui/501/" + serviceLabel,
		"launchctl print gui/501/" + serviceLabel,
		"launchctl enable gui/501/" + serviceLabel,
		"launchctl bootstrap gui/501 " + path,
	}, m.calls)

	plist := readFile(t, path)
	assert.Contains(t, plist, "<array>\n\t\t<string>"+exe+"</string>", "a native executable runs directly")
	assert.NotContains(t, plist, "EnvironmentVariables")
}

func TestALaunchctlFailureEndsTheInstall(t *testing.T) {
	m := &fakeManager{refuse: "bootstrap"}
	s := testService(t, writeExe(t, "recycler", "\x7fELF"), nil, m)
	_, err := s.install("Darwin")
	assert.ErrorContains(t, err, "refused")

	m = &fakeManager{loaded: true, refuse: "bootout"}
	s = testService(t, writeExe(t, "recycler", "\x7fELF"), nil, m)
	_, err = s.install("Darwin")
	assert.ErrorContains(t, err, "refused")
}

// The unit quotes every word, and doubles the % systemd would read as a specifier.
func TestTheSystemdUnitRestartsTheDaemonAlways(t *testing.T) {
	exe := writeExe(t, "re cycler 100%", "jartsr='")
	m := &fakeManager{}
	config := t.TempDir()
	s := testService(t, exe, map[string]string{"XDG_CONFIG_HOME": config, "XDG_DATA_HOME": "/data"}, m)

	path, err := s.install("Linux")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(config, "systemd", "user", systemdUnit), path)

	unit := readFile(t, path)
	escaped := strings.ReplaceAll(exe, "%", "%%")
	assert.Contains(t, unit, `ExecStart="/bin/sh" "`+escaped+`" "daemon" "--standby"`)
	assert.NotContains(t, unit, "Environment=", "the installer's environment would give the daemon its lock and bin")
	assert.Contains(t, unit, "Restart=always\n")
	assert.Contains(t, unit, "WantedBy=default.target\n")

	assert.Equal(t, []string{
		"systemctl --user daemon-reload",
		"systemctl --user enable " + systemdUnit,
		"systemctl --user restart " + systemdUnit,
	}, m.calls)
}

// A relative XDG_CONFIG_HOME is not one, by the specification.
func TestTheSystemdUnitGoesUnderTheHomeConfigByDefault(t *testing.T) {
	m := &fakeManager{refuse: "restart"}
	s := testService(t, writeExe(t, "recycler", "\x7fELF"), map[string]string{"XDG_CONFIG_HOME": "relative"}, m)
	_, err := s.install("Linux")
	assert.ErrorContains(t, err, "refused")
	assert.FileExists(t, filepath.Join(s.home, ".config", "systemd", "user", systemdUnit))
}

// schtasks reads a task definition as UTF-16.
func TestTheScheduledTaskStartsAtLogonAndRestartsOnFailure(t *testing.T) {
	isolateDaemonState(t)
	exe := writeExe(t, "recycler.exe", "MZ")
	m := &fakeManager{}
	s := testService(t, exe, nil, m)

	path, err := s.install("Windows")
	require.NoError(t, err)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, []byte{0xff, 0xfe}, raw[:2], "no UTF-16LE byte order mark")
	units := make([]uint16, 0, len(raw)/2)
	for i := 2; i+1 < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	task := string(utf16.Decode(units))
	assert.Contains(t, task, "<Command>"+exe+"</Command>")
	assert.Contains(t, task, "<Arguments>daemon --standby</Arguments>")
	assert.Contains(t, task, `<UserId>HOST\me</UserId>`)
	assert.Contains(t, task, "<LogonTrigger>")
	assert.Contains(t, task, "<RestartOnFailure>")

	assert.Equal(t, []string{
		"schtasks /Create /TN " + serviceLabel + " /XML " + path + " /F",
		"schtasks /Run /TN " + serviceLabel,
	}, m.calls)
}

func TestASchtasksFailureEndsTheInstall(t *testing.T) {
	isolateDaemonState(t)
	m := &fakeManager{refuse: "/Run"}
	_, err := testService(t, writeExe(t, "recycler.exe", "MZ"), nil, m).install("Windows")
	assert.ErrorContains(t, err, "refused")
}

func TestAnUnknownKernelGetsNoService(t *testing.T) {
	m := &fakeManager{}
	_, err := testService(t, writeExe(t, "recycler", "\x7fELF"), nil, m).install("Plan9")
	assert.ErrorIs(t, err, bin.ErrUnsupported)
	assert.Empty(t, m.calls)
}

func TestAMissingExecutableIsNotInstalled(t *testing.T) {
	m := &fakeManager{}
	_, err := testService(t, filepath.Join(t.TempDir(), "gone"), nil, m).install("Darwin")
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Empty(t, m.calls)

	_, err = testService(t, filepath.Join(t.TempDir(), "gone"), nil, m).install("Linux")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// An executable shorter than the magic is still one to run directly.
func TestAShortExecutableRunsDirectly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiny")
	require.NoError(t, os.WriteFile(path, []byte("MZ"), 0o755))
	args, err := daemonArgs(path)
	require.NoError(t, err)
	assert.Equal(t, []string{path, "daemon", "--standby"}, args)
}

// A failed command says what it printed, which is where a service manager
// explains itself.
func TestAFailedCommandCarriesItsOutput(t *testing.T) {
	require.NoError(t, runCommand("sh", "-c", "exit 0"))
	err := runCommand("sh", "-c", "echo launchd says no >&2; exit 3")
	assert.ErrorContains(t, err, "launchd says no")
}

// Install asks the kernel which service manager to use, and refuses an
// executable that is not there before asking that manager anything.
func TestInstallRefusesAMissingExecutable(t *testing.T) {
	isolateDaemonState(t)
	_, err := Install(filepath.Join(t.TempDir(), "gone"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// newService fills in the user running this.
func TestNewServiceDescribesTheCurrentUser(t *testing.T) {
	isolateDaemonState(t)
	s, err := newService("/bin/recycler")
	require.NoError(t, err)
	assert.Equal(t, os.Getenv("HOME"), s.home)
	assert.Equal(t, os.Getuid(), s.uid)
	log, err := LogPath()
	require.NoError(t, err)
	assert.Equal(t, log, s.log)
}
