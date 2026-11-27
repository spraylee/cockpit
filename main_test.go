package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProcessComposeLifecycle(t *testing.T) {
	if os.Getenv("COCKPIT_INTEGRATION") != "1" {
		t.Skip("set COCKPIT_INTEGRATION=1 to test with the real process-compose binary")
	}
	if _, err := exec.LookPath("process-compose"); err != nil {
		t.Fatal(err)
	}
	// A short path even when macOS TMPDIR or a CI checkout is deeply nested.
	sandbox, err := os.MkdirTemp("/tmp", "cockpit-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sandbox) })
	t.Setenv("COCKPIT_HOME", filepath.Join(sandbox, "state"))
	cockpit := filepath.Join(sandbox, "cockpit")
	if b, err := exec.Command("go", "build", "-o", cockpit, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(cockpit, args...)
		cmd.Dir = sandbox
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("cockpit %q: %v\n%s", args, err, b)
		}
		return string(b)
	}
	t.Cleanup(func() { run("down") })
	if out := run("list", "--json"); out != "[]\n" {
		t.Fatalf("empty list = %q", out)
	}
	if _, err := os.Stat(os.Getenv("COCKPIT_HOME")); !os.IsNotExist(err) {
		t.Fatalf("list unexpectedly created state: %v", err)
	}
	if out, err := exec.Command(cockpit, "ui").CombinedOutput(); err == nil || !strings.Contains(string(out), "后台未运行") {
		t.Fatalf("ui should not start the daemon: %v\n%s", err, out)
	}

	fixture, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argument := `with spaces and "quotes" and $literal`
	a := exec.Command(cockpit, "start", "preview", "--note", "项目预览", "--", fixture, "-test.run=^TestListenerFixture$", "--", argument)
	b := exec.Command(cockpit, "start", "watchdog", "--", "/bin/sleep", "20")
	a.Dir, b.Dir = sandbox, sandbox
	first := make(chan error, 1)
	go func() {
		if out, err := a.CombinedOutput(); err != nil {
			first <- fmt.Errorf("preview start: %w: %s", err, out)
			return
		}
		first <- nil
	}()
	out, err := b.CombinedOutput()
	if errA := <-first; errA != nil {
		t.Fatal(errA)
	}
	if err != nil {
		t.Fatalf("watchdog start: %v\n%s", err, out)
	}
	run("up") // An existing manager must not be replaced.

	list := func() []service {
		t.Helper()
		out := run("list", "--json")
		var items []service
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			t.Fatalf("decode: %v (%s)", err, out)
		}
		return items
	}
	until(t, func() bool {
		items := list()
		return len(items) == 2 && items[0].Name == "preview" && items[0].Running && len(items[0].TCP) > 0 && items[1].Running
	})
	preview := list()[0]
	if preview.Note != "项目预览" || preview.Cwd != sandbox || len(preview.Command) != 4 || preview.Command[3] != argument {
		t.Fatalf("missing process metadata: %+v", preview)
	}
	if table := run("list"); !strings.Contains(table, "DIR") || !strings.Contains(table, filepath.Base(sandbox)) {
		t.Fatalf("list must show the working directory: %q", table)
	}
	if logs := run("logs", "preview"); !strings.Contains(logs, argument) {
		t.Fatalf("argv did not reach program as-is: %q", logs)
	}
	if out, err := exec.Command(cockpit, "start", "preview", "--", "/bin/sh", "-c", "exit 1").CombinedOutput(); err == nil || !strings.Contains(string(out), "已存在") {
		t.Fatalf("duplicate name must not replace service: %v\n%s", err, out)
	}
	if out := run("stop", "preview"); !strings.Contains(out, sandbox) {
		t.Fatalf("stop must confirm which directory it acted on: %q", out)
	}
	until(t, func() bool {
		items := list()
		return len(items) == 2 && !items[0].Running && items[1].Running
	})
	if conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", preview.TCP[0]), time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("the stopped preview still accepts connections")
	}
	run("start", "preview")
	until(t, func() bool { return list()[0].Running })
	run("restart", "preview")
	until(t, func() bool { return list()[0].Running })
	run("rm", "preview")
	items := list()
	if len(items) != 1 || items[0].Name != "watchdog" || !items[0].Running {
		t.Fatalf("rm affected the sibling service: %+v", items)
	}
	run("down")
	run("up")
	if items := list(); len(items) != 0 {
		t.Fatalf("up unexpectedly restored prior tasks: %+v", items)
	}
}

func until(t *testing.T, ready func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if ready() {
			return
		}
	}
	t.Fatal("expected process state did not arrive within 5 seconds")
}

// No dev server: a loopback socket held for at most 20 seconds to test discovery.
func TestListenerFixture(t *testing.T) {
	if len(os.Args) < 4 || os.Args[2] != "--" {
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fmt.Println(os.Args[3])
	time.Sleep(20 * time.Second)
}

func TestShortDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"":                "-",
		home:              "~",
		home + "/code/me": "~/code/me",
		"/tmp/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s": "…/r/s",
	}
	for in, want := range cases {
		if got := shortDir(in); got != want {
			t.Errorf("shortDir(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shortDir("/tmp/cockpit/dir"); got != "/tmp/cockpit/dir" {
		t.Errorf("short path should stay intact, got %q", got)
	}
}

func TestBadInputDoesNotStartDaemon(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "not-used-because-input-is-invalid")
	t.Setenv("COCKPIT_HOME", dir)
	for _, args := range [][]string{{"start", "../foreign", "--", "sleep", "1"}, {"start", "foo", "sleep", "1"}, {"down", "all"}} {
		if err := run(args); err == nil {
			t.Fatalf("accepted invalid argv: %q", args)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		latest, current string
		newer           bool
	}{
		{"v0.3.0", "v0.2.9", true},
		{"v0.2.1", "v0.2.0", true},
		{"v0.2.0", "v0.2.0", false},
		{"v0.1.9", "v0.2.0", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.2.0", "dev", true},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.newer {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.newer)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa  cockpit-v0.2.0-aarch64-apple-darwin.tar.gz\nBBB *cockpit-v0.2.0-x86_64-apple-darwin.tar.gz\n"
	if got, err := checksumFor(sums, "cockpit-v0.2.0-aarch64-apple-darwin.tar.gz"); err != nil || got != "aaa" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := checksumFor(sums, "cockpit-v0.2.0-x86_64-apple-darwin.tar.gz"); err != nil || got != "bbb" {
		t.Fatalf("binary-mode marker not handled: %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "cockpit-v0.2.0-linux.tar.gz"); err == nil {
		t.Fatal("missing asset should fail")
	}
}
