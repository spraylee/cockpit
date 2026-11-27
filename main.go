package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
)

var version = "dev"

const usage = `cockpit — 按需托管本机开发进程（别名 ck）

  cockpit up                                启动后台管理器（不启动旧任务）
  cockpit start NAME [--note TEXT] -- CMD…   新增并启动，使用当前目录
  cockpit start NAME                        启动已登记的服务
  cockpit list [--json]                     查看状态、时长、端口、目录、命令和备注
  cockpit logs NAME                         查看最近 100 行日志
  cockpit stop NAME                         停止，保留登记和日志
  cockpit restart NAME                      重启已登记的服务
  cockpit rm NAME                           停止并移除登记
  cockpit ui                                打开 TUI；F10 或 Ctrl+C 只退出面板
  cockpit down                              停止所有受管服务并退出后台管理器
  cockpit update                            升级到最新版本
  cockpit --version                         显示版本
`

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cockpit:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || slices.Equal(args, []string{"--help"}) || slices.Equal(args, []string{"-h"}) {
		fmt.Print(usage)
		return nil
	}
	if slices.Equal(args, []string{"--version"}) {
		fmt.Println(version)
		return nil
	}
	if args[0] == "update" {
		if len(args) != 1 {
			return errors.New("用法：cockpit update")
		}
		return updateSelf()
	}

	action, rest := args[0], args[1:]
	switch action {
	case "up", "down", "ui":
		if len(rest) != 0 {
			return fmt.Errorf("用法：cockpit %s", action)
		}
	case "list":
		if len(rest) != 0 && !slices.Equal(rest, []string{"--json"}) {
			return errors.New("用法：cockpit list [--json]")
		}
	case "stop", "restart", "logs", "rm", "start":
		if len(rest) == 0 || !validName.MatchString(rest[0]) {
			return errors.New("服务名须以字母或数字开头，只允许字母、数字、点、下划线和连字符，最多 64 字符")
		}
		if action != "start" && len(rest) != 1 {
			return fmt.Errorf("用法：cockpit %s NAME", action)
		}
	default:
		return fmt.Errorf("未知命令 %q；查看 cockpit --help", action)
	}

	var newConfig *processConfig
	if action == "start" && len(rest) > 1 {
		var err error
		newConfig, err = startConfig(rest[0], rest[1:])
		if err != nil {
			return err
		}
	}
	m, err := newManager()
	if err != nil {
		return err
	}

	// Serialize changes, but do not hold the lock while a user is in the TUI.
	switch action {
	case "up", "down", "start", "stop", "restart", "rm":
		if action == "down" {
			up, err := m.isUp()
			if err != nil || !up {
				return err
			}
		}
		unlock, err := m.lock()
		if err != nil {
			return err
		}
		defer unlock()
	}
	if action == "up" || newConfig != nil {
		if err := m.up(); err != nil {
			return err
		}
	}
	if action == "up" {
		fmt.Println("后台已就绪")
		return nil
	}

	up, err := m.isUp()
	if err != nil {
		return err
	}
	if !up {
		switch action {
		case "list":
			return printList(nil, len(rest) != 0)
		case "down":
			return nil
		default:
			return errors.New("后台未运行；先执行 cockpit up，或用 cockpit start NAME -- CMD 新建服务")
		}
	}

	switch action {
	case "down":
		return m.down()
	case "ui":
		bin, err := composeBin()
		if err != nil {
			return err
		}
		return syscall.Exec(bin, []string{bin, "--use-uds", "--unix-socket", m.socket, "--log-file", filepath.Join(m.dir, "ui.log"), "--read-only", "attach"}, m.composeEnv())
	case "list":
		items, err := m.list()
		if err != nil {
			return err
		}
		return printList(items, len(rest) != 0)
	}

	name := rest[0]
	key := url.PathEscape(name)
	switch action {
	case "start":
		if newConfig != nil {
			return m.change(name, newConfig)
		}
		return m.activate("started", "POST", "/process/start/"+key, name)
	case "stop":
		return m.activate("stopped", "PATCH", "/process/stop/"+key, name)
	case "restart":
		return m.activate("restarted", "POST", "/process/restart/"+key, name)
	case "rm":
		return m.change(name, nil)
	case "logs":
		var result struct{ Logs []string }
		if err := m.call("GET", "/process/logs/"+key+"/0/100", nil, &result); err != nil {
			return err
		}
		for _, line := range result.Logs {
			fmt.Println(line)
		}
	}
	return nil
}

func startConfig(name string, args []string) (*processConfig, error) {
	note := ""
	if len(args) >= 2 && args[0] == "--note" {
		note, args = args[1], args[2:]
	}
	if len(args) < 2 || args[0] != "--" {
		return nil, errors.New("用法：cockpit start NAME [--note TEXT] -- CMD [ARGS…]")
	}
	argv := args[1:]
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, fmt.Errorf("找不到可执行命令 %q：%w", argv[0], err)
	}
	bin, err = filepath.Abs(bin)
	if err != nil {
		return nil, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &processConfig{
		Name: name, ReplicaName: name, Replicas: 1,
		Entrypoint: argv, Executable: bin, Args: argv[1:],
		WorkingDir: cwd, Description: note, Environment: []string{"PATH=" + os.Getenv("PATH")},
		Shutdown: shutdownParams{Timeout: 5, Signal: int(syscall.SIGTERM)},
	}, nil
}

func printList(items []service, asJSON bool) error {
	if asJSON {
		if items == nil {
			items = []service{}
		}
		return json.NewEncoder(os.Stdout).Encode(items)
	}
	if len(items) == 0 {
		fmt.Println("没有托管服务")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATUS\tAGE\tPORTS\tDIR\tCOMMAND\tNOTE")
	for _, item := range items {
		ports := make([]string, 0, len(item.TCP)+len(item.UDP))
		for _, port := range item.TCP {
			ports = append(ports, fmt.Sprintf("%d/tcp", port))
		}
		for _, port := range item.UDP {
			ports = append(ports, fmt.Sprintf("%d/udp", port))
		}
		argv := make([]string, len(item.Command))
		for i, arg := range item.Command {
			argv[i] = display(arg)
			if arg == "" || strings.ContainsAny(arg, " \t\n\r") {
				argv[i] = strconv.Quote(arg)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", display(item.Name), item.Status, item.Age, strings.Join(ports, ","), shortDir(item.Cwd), strings.Join(argv, " "), display(item.Note))
	}
	return w.Flush()
}

// The DIR column has to identify a project at a glance rather than show a
// full path: a service without its directory cannot be safely stopped, and
// full paths push COMMAND off narrow terminals. JSON keeps the exact path.
func shortDir(dir string) string {
	if dir == "" {
		return "-"
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		switch {
		case dir == home:
			return "~"
		case strings.HasPrefix(dir, home+string(os.PathSeparator)):
			dir = "~" + dir[len(home):]
		}
	}
	const budget = 34
	runes := []rune(dir)
	if len(runes) <= budget {
		return dir
	}
	parts := strings.Split(dir, string(os.PathSeparator))
	if len(parts) >= 2 {
		tail := "…/" + strings.Join(parts[len(parts)-2:], string(os.PathSeparator))
		if len([]rune(tail)) <= budget {
			return tail
		}
	}
	return "…" + string(runes[len(runes)-(budget-1):])
}

func display(s string) string {
	q := strconv.Quote(s)
	return q[1 : len(q)-1]
}
