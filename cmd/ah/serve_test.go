package main

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestLaunchdPlist(t *testing.T) {
	p := launchdPlist("io.example.hub", "/Users/a b/bin/agenthub-server", []string{"--listen", "127.0.0.1:9000", "--data", "/tmp/d&e"}, "/tmp/hub.log")
	// Must be well-formed XML, including the escaped ampersand and the space in the path.
	if err := xml.Unmarshal([]byte(p), new(struct{})); err != nil {
		t.Fatalf("plist is not valid XML: %v\n%s", err, p)
	}
	for _, want := range []string{
		"<string>io.example.hub</string>",
		"<string>/Users/a b/bin/agenthub-server</string>",
		"<string>--listen</string>", "<string>127.0.0.1:9000</string>",
		"<string>/tmp/d&amp;e</string>",
		"<key>RunAtLoad</key>\n  <true/>", "<key>KeepAlive</key>\n  <true/>",
		"<key>StandardOutPath</key>\n  <string>/tmp/hub.log</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q:\n%s", want, p)
		}
	}
}

func TestSystemdUnit(t *testing.T) {
	u := systemdUnit("/home/me/.local/bin/agenthub-server", nil)
	if !strings.Contains(u, "ExecStart=/home/me/.local/bin/agenthub-server\n") || !strings.Contains(u, "Restart=on-failure") || !strings.Contains(u, "WantedBy=default.target") {
		t.Errorf("unexpected unit:\n%s", u)
	}
	u = systemdUnit("/opt/my hub/agenthub-server", []string{"--data", "/data/100%"})
	if !strings.Contains(u, `ExecStart="/opt/my hub/agenthub-server" --data "/data/100%%"`) {
		t.Errorf("paths with spaces and percent signs must be quoted:\n%s", u)
	}
}

func TestWindowsTaskCommand(t *testing.T) {
	got := windowsTaskCommand(`C:\Users\me\bin\agenthub-server.exe`, []string{"--listen", ":9000"})
	if got != `"C:\Users\me\bin\agenthub-server.exe" "--listen" ":9000"` {
		t.Errorf("got %s", got)
	}
}

func TestServerFlags(t *testing.T) {
	if got := serverFlags("", ""); len(got) != 0 {
		t.Errorf("no flags expected, got %v", got)
	}
	got := serverFlags(":9000", "/d")
	if strings.Join(got, " ") != "--listen :9000 --data /d" {
		t.Errorf("got %v", got)
	}
}
