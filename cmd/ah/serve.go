package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// `ah serve` keeps the hub running across logins and reboots by registering it
// with the operating system's own service manager: launchd on macOS, a systemd
// user unit on Linux, and Task Scheduler on Windows. No admin rights needed.

const (
	serviceLabel = "io.github.its-ammu.agenthub" // launchd label
	serviceName  = "agenthub"                    // systemd unit and Windows task name
)

func cmdServe(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "install":
		serveInstall(args)
	case "uninstall":
		serveUninstall()
	case "status":
		serveStatus()
	default:
		fmt.Fprintln(os.Stderr, "usage: ah serve [install [--listen ADDR] [--data DIR] [--bin PATH] [--print] | uninstall | status]")
		os.Exit(1)
	}
}

func hubLogPath() string { return filepath.Join(configDir(), "hub.log") }

// serverBinary finds agenthub-server: an explicit path, the one next to this ah, or on PATH.
func serverBinary(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	name := "agenthub-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), name); fileOK(p) {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return filepath.Abs(p)
	}
	return "", fmt.Errorf("cannot find %s next to ah or on PATH (use --bin PATH)", name)
}

func fileOK(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func serverFlags(listen, data string) []string {
	var a []string
	if listen != "" {
		a = append(a, "--listen", listen)
	}
	if data != "" {
		a = append(a, "--data", data)
	}
	return a
}

// ---- service definitions (pure, so they can be tested) ----

func launchdPlist(label, bin string, args []string, logPath string) string {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var prog strings.Builder
	prog.WriteString("    <string>" + esc(bin) + "</string>\n")
	for _, a := range args {
		prog.WriteString("    <string>" + esc(a) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + esc(label) + `</string>
  <key>ProgramArguments</key>
  <array>
` + prog.String() + `  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>` + esc(logPath) + `</string>
  <key>StandardErrorPath</key>
  <string>` + esc(logPath) + `</string>
</dict>
</plist>
`
}

// systemdQuote quotes a word for an ExecStart= line when it needs it.
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s)
	return `"` + s + `"`
}

func systemdUnit(bin string, args []string) string {
	words := []string{systemdQuote(bin)}
	for _, a := range args {
		words = append(words, systemdQuote(a))
	}
	return `[Unit]
Description=AgentHub blackboard for coding agents
After=network.target

[Service]
ExecStart=` + strings.Join(words, " ") + `
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`
}

// windowsTaskCommand is the command line Task Scheduler runs at logon.
func windowsTaskCommand(bin string, args []string) string {
	parts := []string{`"` + bin + `"`}
	for _, a := range args {
		parts = append(parts, `"`+a+`"`)
	}
	return strings.Join(parts, " ")
}

func launchdPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist")
}

func systemdPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user", serviceName+".service")
}

// ---- commands ----

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func hubUp() bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := c.Get(serverURL() + "/api/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func serveInstall(args []string) {
	fs := flag.NewFlagSet("serve install", flag.ExitOnError)
	listen := fs.String("listen", "", "listen address for the hub (default :8080)")
	data := fs.String("data", "", "data directory (default ~/.agenthub/data)")
	bin := fs.String("bin", "", "path to agenthub-server (default: next to ah, or on PATH)")
	printOnly := fs.Bool("print", false, "print the service definition and exit without installing")
	fs.Parse(args)

	server, err := serverBinary(*bin)
	if err != nil {
		fatal("%v", err)
	}
	flags := serverFlags(*listen, *data)
	os.MkdirAll(configDir(), 0700)

	switch runtime.GOOS {
	case "darwin":
		plist := launchdPlist(serviceLabel, server, flags, hubLogPath())
		if *printOnly {
			fmt.Printf("# %s\n%s", launchdPath(), plist)
			return
		}
		warnIfRunning()
		path := launchdPath()
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(plist), 0644); err != nil {
			fatal("cannot write %s: %v", path, err)
		}
		domain := "gui/" + strconv.Itoa(os.Getuid())
		run("launchctl", "bootout", domain+"/"+serviceLabel) // ignore: not loaded yet
		if out, err := run("launchctl", "bootstrap", domain, path); err != nil {
			fatal("launchctl bootstrap failed: %v\n%s", err, out)
		}
		fmt.Printf("installed %s\n", path)
	case "linux":
		unit := systemdUnit(server, flags)
		if *printOnly {
			fmt.Printf("# %s\n%s", systemdPath(), unit)
			return
		}
		warnIfRunning()
		path := systemdPath()
		os.MkdirAll(filepath.Dir(path), 0755)
		if err := os.WriteFile(path, []byte(unit), 0644); err != nil {
			fatal("cannot write %s: %v", path, err)
		}
		if out, err := run("systemctl", "--user", "daemon-reload"); err != nil {
			fatal("systemctl daemon-reload failed: %v\n%s", err, out)
		}
		if out, err := run("systemctl", "--user", "enable", "--now", serviceName+".service"); err != nil {
			fatal("systemctl enable failed: %v\n%s", err, out)
		}
		fmt.Printf("installed %s\n", path)
	case "windows":
		cmdline := windowsTaskCommand(server, flags)
		if *printOnly {
			fmt.Printf("schtasks /Create /SC ONLOGON /TN %s /TR %s /F\n", serviceName, cmdline)
			return
		}
		warnIfRunning()
		if out, err := run("schtasks", "/Create", "/SC", "ONLOGON", "/TN", serviceName, "/TR", cmdline, "/F"); err != nil {
			fatal("schtasks create failed: %v\n%s", err, out)
		}
		run("schtasks", "/Run", "/TN", serviceName)
		fmt.Printf("registered the %q task to run at logon\n", serviceName)
	default:
		fatal("run-at-login is not supported on %s; start agenthub-server yourself", runtime.GOOS)
	}

	// Give the hub a moment to come up, then confirm.
	for i := 0; i < 10; i++ {
		if hubUp() {
			fmt.Printf("the hub is running at %s (log: %s)\n", serverURL(), hubLogPath())
			if *listen != "" {
				fmt.Printf("you chose --listen %s: point agents at it with AH_SERVER or ~/.agenthub/server\n", *listen)
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("installed, but the hub did not answer at %s yet. Check the log: %s\n", serverURL(), hubLogPath())
}

func warnIfRunning() {
	if hubUp() {
		fmt.Printf("note: a hub is already answering at %s. Stop it first, or the service cannot bind the port.\n", serverURL())
	}
}

func serveUninstall() {
	switch runtime.GOOS {
	case "darwin":
		path := launchdPath()
		run("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+serviceLabel)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fatal("cannot remove %s: %v", path, err)
		}
		fmt.Println("removed the launchd agent; the hub no longer starts at login")
	case "linux":
		run("systemctl", "--user", "disable", "--now", serviceName+".service")
		if err := os.Remove(systemdPath()); err != nil && !os.IsNotExist(err) {
			fatal("cannot remove %s: %v", systemdPath(), err)
		}
		run("systemctl", "--user", "daemon-reload")
		fmt.Println("removed the systemd user unit; the hub no longer starts at login")
	case "windows":
		run("schtasks", "/End", "/TN", serviceName)
		run("schtasks", "/Delete", "/TN", serviceName, "/F")
		fmt.Println("removed the scheduled task; the hub no longer starts at logon")
	default:
		fatal("run-at-login is not supported on %s", runtime.GOOS)
	}
}

func serveStatus() {
	registered := false
	switch runtime.GOOS {
	case "darwin":
		registered = fileOK(launchdPath())
	case "linux":
		registered = fileOK(systemdPath())
	case "windows":
		_, err := run("schtasks", "/Query", "/TN", serviceName)
		registered = err == nil
	}
	if registered {
		fmt.Println("run at login: yes")
	} else {
		fmt.Println("run at login: no (enable it with `ah serve install`)")
	}
	if hubUp() {
		fmt.Printf("hub: running at %s\n", serverURL())
	} else {
		fmt.Printf("hub: not answering at %s\n", serverURL())
	}
	if fileOK(hubLogPath()) {
		fmt.Printf("log: %s\n", hubLogPath())
	}
}
