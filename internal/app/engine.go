package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"android-tools/internal/tooldeps"
)

const adbSocket = "tcp:127.0.0.1:50387"

type device struct{ Serial, State, Model, Kind, Hardware string }
type service struct{ Name, Kind, Address string }

var adbDeviceLine = regexp.MustCompile(`^(.+?)\s+(device|offline|unauthorized|recovery|sideload|bootloader|no permissions)(?:\s+(.*))?$`)

type engine struct {
	adb, fastboot, scrcpy string
	server                *exec.Cmd
	serverMu              sync.Mutex
	serverDone            chan error
	execute               func(string, time.Duration, string, ...string) (string, error)
	mu                    sync.Mutex
	mirrors               map[string]*exec.Cmd
	mirrorErrors          map[string]string
	stoppingMirrors       map[*exec.Cmd]bool
	group                 *processGroup
	socket                string
	serverUSB             string
}

func newEngine() *engine {
	exe, _ := os.Executable()
	dir := filepath.Join(filepath.Dir(exe), tooldeps.Directory)
	return &engine{adb: filepath.Join(dir, "adb.exe"), fastboot: filepath.Join(dir, "fastboot.exe"), scrcpy: filepath.Join(dir, "scrcpy.exe"), mirrors: map[string]*exec.Cmd{}, mirrorErrors: map[string]string{}, serverUSB: os.Getenv("ANDROID_TOOLS_ADB_USB_SERIAL")}
}
func (e *engine) command(ctx context.Context, tool string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), "ADB_SERVER_SOCKET="+e.socketAddress(), "ADB="+e.adb)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
func (e *engine) socketAddress() string {
	if e.socket != "" {
		return e.socket
	}
	return adbSocket
}
func (e *engine) run(tool string, timeout time.Duration, input string, args ...string) (string, error) {
	if e.execute != nil {
		return e.execute(tool, timeout, input, args...)
	}
	if tool == "" {
		return "", errors.New("未找到组件，请点击检查并下载组件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := e.command(ctx, tool, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input + "\n")
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", errors.New("操作超时，请检查连接")
	}
	if err != nil {
		if input != "" {
			return "", errors.New("配对失败，请检查配对地址和配对码")
		}
		return "", fmt.Errorf("操作失败：%s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
func (e *engine) start() error {
	e.serverMu.Lock()
	defer e.serverMu.Unlock()
	if e.serverDone != nil {
		select {
		case <-e.serverDone:
			e.serverDone = nil
			e.server = nil
		default:
			return nil
		}
	}
	if e.adb == "" {
		return errors.New("未找到 adb.exe，请点击检查并下载组件")
	}
	if _, err := os.Stat(e.adb); err != nil {
		return errors.New("未找到 adb.exe，请点击检查并下载组件")
	}
	address := strings.TrimPrefix(e.socketAddress(), "tcp:")
	l, err := net.Listen("tcp", address)
	if err != nil {
		return errors.New("专用 ADB 端口已占用，请关闭其他本程序实例")
	}
	l.Close()
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("ADB 服务地址无效")
	}
	args := []string{"-L", "tcp:" + port}
	if e.serverUSB != "" {
		args = append(args, "--one-device", e.serverUSB)
	}
	args = append(args, "server", "nodaemon")
	e.server = e.command(context.Background(), e.adb, args...)
	if err = e.server.Start(); err != nil {
		return err
	}
	if err = e.group.attach(e.server.Process); err != nil {
		_ = e.server.Process.Kill()
		_ = e.server.Wait()
		e.server = nil
		return fmt.Errorf("无法保护设备服务进程：%w", err)
	}
	e.serverDone = make(chan error, 1)
	cmd, done := e.server, e.serverDone
	go func() { done <- cmd.Wait() }()
	for i := 0; i < 100; i++ {
		select {
		case <-done:
			e.server = nil
			e.serverDone = nil
			return errors.New("ADB 服务启动失败")
		default:
		}
		c, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_, err = c.Write([]byte("000chost:version"))
			reply := make([]byte, 4)
			if err == nil {
				_, err = io.ReadFull(c, reply)
			}
			c.Close()
			if err == nil && string(reply) == "OKAY" {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = e.server.Process.Kill()
	<-done
	e.server = nil
	e.serverDone = nil
	return errors.New("ADB 服务启动超时")
}
func (e *engine) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.mirrors {
		if p.Process != nil {
			_ = p.Process.Kill()
		}
	}
	e.serverMu.Lock()
	defer e.serverMu.Unlock()
	if e.server != nil && e.server.Process != nil {
		_ = e.server.Process.Kill()
		if e.serverDone != nil {
			<-e.serverDone
		}
		e.server = nil
		e.serverDone = nil
	}
}
func (e *engine) list() ([]device, error) {
	result, adbErr := e.adbDevices()
	if e.fastboot != "" {
		fastboot, _ := e.listFastboot()
		result = append(result, fastboot...)
	}
	return result, adbErr
}
func (e *engine) adbDevices() ([]device, error) {
	out, err := e.run(e.adb, 5*time.Second, "", "devices", "-l")
	return parseADBDevices(out), err
}
func (e *engine) listFastboot() ([]device, error) {
	if e.fastboot == "" {
		return nil, nil
	}
	out, err := e.run(e.fastboot, 5*time.Second, "", "devices", "-l")
	if err != nil {
		return nil, err
	}
	var result []device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			result = append(result, device{Serial: f[0], State: f[1], Kind: "Fastboot"})
		}
	}
	return result, nil
}
func validAddress(s string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return errors.New("请输入 IP:端口")
	}
	ip := net.ParseIP(strings.Split(host, "%")[0])
	if ip == nil || (!ip.IsPrivate() && !ip.IsLinkLocalUnicast()) {
		return errors.New("仅支持局域网 IP 地址")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("端口无效")
	}
	return nil
}
func (e *engine) pairSecret(addr, code string) (string, error) {
	if err := validAddress(addr); err != nil {
		return "", err
	}
	out, err := e.run(e.adb, 15*time.Second, code, "pair", addr)
	if err != nil {
		return "", err
	}
	if !strings.Contains(strings.ToLower(out), "successfully paired") {
		return "", errors.New("配对失败，请检查配对码和地址")
	}
	pos := strings.Index(out, "guid=")
	if pos < 0 {
		return "", errors.New("配对成功但未返回设备身份，请重试")
	}
	guid := strings.FieldsFunc(out[pos+5:], func(r rune) bool { return r == ']' || r == ')' || r == ' ' || r == '\n' || r == '\r' })
	if len(guid) == 0 || guid[0] == "" {
		return "", errors.New("未能读取配对身份")
	}
	return guid[0], nil
}
func (e *engine) connect(addr string) error {
	if err := validAddress(addr); err != nil {
		return err
	}
	out, err := e.run(e.adb, 8*time.Second, "", "connect", addr)
	if err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(out), "connected to") {
		return errors.New("连接失败，请使用无线调试首页的连接端口")
	}
	return nil
}
func (e *engine) services() []service {
	out, err := e.run(e.adb, 5*time.Second, "", "mdns", "services")
	if err != nil {
		return nil
	}
	var list []service
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 3 {
			continue
		}
		kind := strings.TrimSuffix(f[len(f)-2], ".")
		addr := f[len(f)-1]
		if (kind == "_adb-tls-pairing._tcp" || kind == "_adb-tls-connect._tcp") && validAddress(addr) == nil {
			list = append(list, service{Name: strings.TrimSuffix(strings.Join(f[:len(f)-2], " "), "."), Kind: kind, Address: addr})
		}
	}
	return list
}
func (e *engine) mirror(serial string, audio bool, size, fps string) error {
	if e.scrcpy == "" {
		return errors.New("未找到 scrcpy.exe，请放在程序目录或 scrcpy 文件夹")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.mirrors[serial]; ok {
		return errors.New("该设备的画面已打开")
	}
	if size != "720" && size != "1280" && size != "1920" && size != "2560" {
		return errors.New("画面尺寸无效")
	}
	if fps != "15" && fps != "30" && fps != "60" {
		return errors.New("画面帧率无效")
	}
	args := []string{"--serial", serial, "--max-size", size, "--max-fps", fps}
	if !audio {
		args = append(args, "--no-audio")
	}
	cmd := e.command(context.Background(), e.scrcpy, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := e.group.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("无法保护投屏进程：%w", err)
	}
	e.mirrors[serial] = cmd
	delete(e.mirrorErrors, serial)
	go func() {
		err := cmd.Wait()
		e.mu.Lock()
		if e.mirrors[serial] == cmd {
			delete(e.mirrors, serial)
		}
		stopping := e.stoppingMirrors[cmd]
		delete(e.stoppingMirrors, cmd)
		if err != nil && !stopping {
			if e.mirrorErrors == nil {
				e.mirrorErrors = map[string]string{}
			}
			e.mirrorErrors[serial] = "投屏已退出，请检查手机授权；声音失败时可关闭声音重试"
		}
		e.mu.Unlock()
	}()
	return nil
}
func (e *engine) isMirroring(serial string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.mirrors[serial]
	return ok
}
func (e *engine) stopMirror(serial string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.mirrors[serial]
	if p == nil || p.Process == nil {
		return errors.New("画面已关闭")
	}
	if e.stoppingMirrors == nil {
		e.stoppingMirrors = map[*exec.Cmd]bool{}
	}
	e.stoppingMirrors[p] = true
	err := p.Process.Kill()
	if err != nil {
		delete(e.stoppingMirrors, p)
	}
	return err
}
func (e *engine) mirrorError(serial string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mirrorErrors[serial]
}
