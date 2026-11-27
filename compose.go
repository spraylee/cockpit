package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const projectName = "cockpit"
const emptyProject = "name: cockpit\nversion: '0.5'\nprocesses: {}\n"

// Field names match the Process Compose 1.124 API, not its YAML keys.
type processConfig struct {
	Name        string         `json:"name"`
	ReplicaName string         `json:"replicaName"`
	Replicas    int            `json:"replicas"`
	Entrypoint  []string       `json:"entrypoint"`
	Executable  string         `json:"executable"`
	Args        []string       `json:"args"`
	WorkingDir  string         `json:"workingDir"`
	Description string         `json:"description,omitempty"`
	Environment []string       `json:"environment"`
	Shutdown    shutdownParams `json:"shutDownParams"`
}

type shutdownParams struct {
	Timeout int `json:"shutDownTimeout"`
	Signal  int `json:"signal"`
}

type processState struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Age      string `json:"system_time"`
	Running  bool   `json:"is_running"`
	ExitCode int    `json:"exit_code"`
}

type service struct {
	processState
	Command []string `json:"command"`
	Cwd     string   `json:"cwd"`
	Note    string   `json:"note"`
	TCP     []uint16 `json:"tcp_ports"`
	UDP     []uint16 `json:"udp_ports"`
}

type manager struct {
	dir    string
	socket string
	client *http.Client
}

func newManager() (*manager, error) {
	dir := os.Getenv("COCKPIT_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".cockpit")
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("COCKPIT_HOME 必须是绝对路径")
	}
	dir = filepath.Clean(dir)
	socket := filepath.Join(dir, "control.sock")
	if len(socket) >= 104 {
		return nil, errors.New("Unix socket 路径过长；请将 COCKPIT_HOME 指向更短的私有目录")
	}
	m := &manager{dir: dir, socket: socket}
	m.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
		}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	if _, err := m.privateDir(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *manager) privateDir() (bool, error) {
	info, err := os.Lstat(m.dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return false, fmt.Errorf("%s 必须是当前用户独占（0700）的目录，且不能是符号链接", m.dir)
	}
	return true, nil
}

func (m *manager) lock() (func(), error) {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, err
	}
	if _, err := m.privateDir(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(m.dir, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

func (m *manager) isUp() (bool, error) {
	info, err := os.Lstat(m.socket)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return false, fmt.Errorf("%s 不是 socket；不会覆盖它", m.socket)
	}
	var identity struct{ ProjectName string }
	err = m.call("GET", "/project/name", nil, &identity)
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("无法确认后台身份，不会启动或停止实例：%w", err)
	}
	if !strings.HasSuffix(identity.ProjectName, "/"+projectName) {
		return false, fmt.Errorf("socket 上是其他项目 %q；不会接管", identity.ProjectName)
	}
	return true, nil
}

func composeBin() (string, error) {
	bin, err := exec.LookPath("process-compose")
	if err != nil {
		return "", errors.New("找不到 process-compose；请先按 README 安装 1.124 或更新版本")
	}
	return filepath.Abs(bin)
}

func (m *manager) composeEnv() []string {
	env := []string{"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, key := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL", "TERM", "COLORTERM", "TERMINFO", "TERMINFO_DIRS"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func (m *manager) up() error {
	if up, err := m.isUp(); up || err != nil {
		return err
	}
	bin, err := composeBin()
	if err != nil {
		return err
	}
	config := filepath.Join(m.dir, "base.yaml")
	if err := os.WriteFile(config, []byte(emptyProject), 0600); err != nil {
		return err
	}
	bootLog := filepath.Join(m.dir, "startup.log")
	log, err := os.OpenFile(bootLog, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(bin, "--use-uds", "--unix-socket", m.socket, "--log-file", filepath.Join(m.dir, "daemon.log"),
		"up", "--tui=false", "--keep-project", "--disable-dotenv", "-f", config)
	cmd.Dir, cmd.Env = m.dir, m.composeEnv()
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.After(5 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return fmt.Errorf("后台未就绪就退出（%v）；查看 %s", err, bootLog)
		case <-timer:
			_ = cmd.Process.Signal(syscall.SIGTERM)
			return fmt.Errorf("后台启动超时；查看 %s", bootLog)
		case <-ticker.C:
			if up, err := m.isUp(); err != nil || up {
				if err != nil {
					_ = cmd.Process.Signal(syscall.SIGTERM)
				}
				return err
			}
		}
	}
}

func (m *manager) down() error {
	if err := m.call("POST", "/project/stop", nil, nil); err != nil {
		return err
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if up, err := m.isUp(); !up || err != nil {
			if err == nil {
				fmt.Println("后台已停止")
			}
			return err
		}
	}
	return errors.New("后台仍未停止；请检查运行日志，不会强制杀进程")
}

func (m *manager) states() ([]processState, error) {
	var result struct{ Data []processState }
	err := m.call("GET", "/processes", nil, &result)
	return result.Data, err
}

func (m *manager) list() ([]service, error) {
	states, err := m.states()
	if err != nil {
		return nil, err
	}
	sort.Slice(states, func(i, j int) bool { return states[i].Name < states[j].Name })
	items := make([]service, 0, len(states))
	for _, state := range states {
		key := url.PathEscape(state.Name)
		var config processConfig
		if err := m.call("GET", "/process/info/"+key, nil, &config); err != nil {
			return nil, err
		}
		item := service{processState: state, Command: config.Entrypoint, Cwd: config.WorkingDir, Note: config.Description}
		if state.Status == "Completed" && state.ExitCode > 0 {
			item.Status = "Failed"
		}
		if state.Running {
			// A process can exit between getting its state and sampling sockets.
			_ = m.call("GET", "/process/ports/"+key, nil, &item)
		}
		items = append(items, item)
	}
	return items, nil
}

func (m *manager) change(name string, config *processConfig) error {
	states, err := m.states()
	if err != nil {
		return err
	}
	project := struct {
		Processes map[string]json.RawMessage
	}{Processes: make(map[string]json.RawMessage, len(states)+1)}
	dir := ""
	if config != nil {
		dir = config.WorkingDir
	}
	found := false
	for _, state := range states {
		if state.Name == name {
			found = true
			if config != nil {
				return fmt.Errorf("服务 %q 已存在；用 cockpit start %s 启动原定义", name, name)
			}
			var mine processConfig
			if err := m.call("GET", "/process/info/"+url.PathEscape(name), nil, &mine); err == nil {
				dir = mine.WorkingDir
			}
			continue
		}
		var existing json.RawMessage
		if err := m.call("GET", "/process/info/"+url.PathEscape(state.Name), nil, &existing); err != nil {
			return err
		}
		project.Processes[state.Name] = existing
	}
	want := "removed"
	if config != nil {
		definition, err := json.Marshal(config)
		if err != nil {
			return err
		}
		project.Processes[name] = definition
		want = "added"
	} else if !found {
		return fmt.Errorf("找不到服务 %q", name)
	}
	var result map[string]string
	if err := m.call("POST", "/project", project, &result); err != nil {
		return err
	}
	if result[name] != want {
		return fmt.Errorf("后台未确认 %s %q；执行 cockpit list 检查", want, name)
	}
	fmt.Printf("%s: %s (%s)\n", want, name, shortDir(dir))
	return nil
}

// Confirming the directory makes a stop verifiable: the name alone cannot tell
// a caller that this is the project it meant to act on.
func (m *manager) activate(label, method, path, name string) error {
	var info processConfig
	if err := m.call("GET", "/process/info/"+url.PathEscape(name), nil, &info); err != nil {
		return err
	}
	if err := m.call(method, path, nil, nil); err != nil {
		return err
	}
	fmt.Printf("%s: %s (%s)\n", label, name, shortDir(info.WorkingDir))
	return nil
}

func (m *manager) call(method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, "http://local"+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("process-compose %s %s：%s %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if output != nil {
		return json.NewDecoder(resp.Body).Decode(output)
	}
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}
