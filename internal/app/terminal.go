package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
)

type terminalState struct {
	kind, serial, input, output string
	mode                        int
	running, shell              bool
	generation                  uint64
	cancel                      context.CancelFunc
	stdin                       chan string
	scroll                      ui.ScrollState
	history                     []string
	historyIndex                int
	historyDraft                string
}

var sendIcon = icon(`<path d="M12 19V5M6 11l6-6 6 6"/>`)
var stopIcon = icon(`<rect x="6" y="6" width="12" height="12" rx="1"/>`)
var copyIcon = icon(`<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/>`)

func (a *app) openTerminal(kind, serial string) {
	a.stopTerminal()
	a.terminal = terminalState{kind: kind, serial: serial, generation: a.terminal.generation + 1}
	a.page = "terminal"
}
func (a *app) terminalOnline() bool {
	for _, d := range a.devices {
		if d.Kind == a.terminal.kind && d.Serial == a.terminal.serial {
			return (d.Kind == "ADB" && d.State == "device") || (d.Kind == "Fastboot" && d.State == "fastboot")
		}
	}
	return false
}
func (a *app) checkTerminalDevice() {
	if a.terminal.running && !a.terminalOnline() {
		a.stopTerminal()
		a.terminal.output += "\n设备已断开，命令已停止\n"
	}
}
func (a *app) stopTerminal() {
	if a.terminal.cancel != nil {
		a.terminal.cancel()
	}
}

func (t *terminalState) rememberCommand(line string) {
	if line == "" {
		return
	}
	if len(t.history) == 0 || t.history[len(t.history)-1] != line {
		t.history = append(t.history, line)
	}
	if len(t.history) > 100 {
		t.history = t.history[len(t.history)-100:]
	}
	t.historyIndex, t.historyDraft = len(t.history), ""
}
func (t *terminalState) navigateHistory(direction int) {
	if len(t.history) == 0 {
		return
	}
	if t.historyIndex < 0 || t.historyIndex > len(t.history) {
		t.historyIndex = len(t.history)
	}
	if direction < 0 {
		if t.historyIndex == len(t.history) {
			t.historyDraft = t.input
		}
		if t.historyIndex > 0 {
			t.historyIndex--
		}
	} else if direction > 0 && t.historyIndex < len(t.history) {
		t.historyIndex++
	}
	if t.historyIndex == len(t.history) {
		t.input = t.historyDraft
	} else {
		t.input = t.history[t.historyIndex]
	}
}
func (t *terminalState) appendFiles(paths []string) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if t.input != "" && !strings.HasSuffix(t.input, " ") {
			t.input += " "
		}
		t.input += windows.EscapeArg(path)
	}
}

func (t *terminalState) receiveFiles(data transfer.Data) error {
	paths, err := data.Files()
	if err != nil || len(paths) == 0 {
		return errors.New("无法读取拖入的文件")
	}
	if t.kind == "ADB" && t.mode == 1 {
		return errors.New("设备 Shell 不能使用电脑文件路径")
	}
	t.appendFiles(paths)
	return nil
}

// parseTerminalArgs uses Windows command-line quoting, without invoking a host shell.
func parseTerminalArgs(input, kind string) ([]string, error) {
	args, err := windows.DecomposeCommandLine(strings.TrimSpace(input))
	if err != nil || len(args) == 0 {
		return nil, errors.New("请输入有效命令")
	}
	if strings.EqualFold(args[0], kind) || strings.EqualFold(args[0], kind+".exe") {
		args = args[1:]
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return nil, errors.New("请输入命令，设备由应用指定")
	}
	if kind == "ADB" {
		switch strings.ToLower(args[0]) {
		case "start-server", "kill-server", "connect", "disconnect", "pair", "server", "devices", "track-devices":
			return nil, errors.New("请在设备管理页面执行此操作")
		}
	}
	return args, nil
}
func riskyFastboot(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch strings.ToLower(args[0]) {
	case "getvar", "reboot", "reboot-bootloader", "continue", "help", "version":
		return false
	default:
		return true
	}
}
func (a *app) terminalView(c *ui.Context) {
	t, term := c.Theme(), &a.terminal
	ui.Column(c).Grow(1).MinHeight(0).Gap(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if a.button(c, "terminal-back", backIcon, "返回设备操作") {
				a.stopTerminal()
				if term.kind == "ADB" {
					a.openADB(term.serial)
				} else {
					a.openFastboot(term.serial)
				}
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, term.kind+" 终端").FontSize(20).Bold()
				status := "已连接"
				if !a.terminalOnline() {
					status = "未连接"
				}
				ui.Text(c, term.serial+" · "+status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if a.button(c, "terminal-copy", copyIcon, "复制输出") {
				c.WriteClipboard(term.output)
			}
			if a.button(c, "terminal-clear", trash, "清空输出") {
				term.output = ""
			}
			if term.running && a.button(c, "terminal-stop", stopIcon, "停止命令") {
				a.stopTerminal()
			}
		})
		if term.kind == "ADB" && !term.running {
			ui.Segmented(c, &term.mode, "ADB 命令", "设备 Shell").Label("输入方式")
		}
		if term.kind == "ADB" && term.mode == 1 {
			ui.Text(c, "设备 Shell：逐行输入，输入 exit 结束会话；暂不支持全屏交互程序。").FontSize(12).TextColor(t.TextMuted)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).TrackScroll(&term.scroll).Label("命令输出").Radius(10).Background(t.Surface).Children(func() {
			ui.Column(c).Padding(14).Children(func() {
				if term.output == "" {
					ui.Text(c, "命令输出将在这里显示").FontSize(12).TextColor(t.TextMuted)
				} else {
					ui.Text(c, term.output).FontSize(12).Selectable()
				}
			})
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			input := ui.TextInput(c, &term.input).Grow(1).Label("命令").Placeholder("输入命令，可拖入文件").OnSubmit(func() { a.submitTerminal() })
			input.OnShortcut(0, ui.KeyUp, func() { term.navigateHistory(-1) }).OnShortcut(0, ui.KeyDown, func() { term.navigateHistory(1) })
			input.OnShortcut(ui.Ctrl, ui.KeyL, func() { term.output = "" }).OnShortcut(0, ui.KeyEscape, func() {
				if term.running {
					a.stopTerminal()
				}
			})
			opts := transfer.DropOptions{Formats: []transfer.Format{transfer.FileList, transfer.URIList}, Operations: transfer.Copy}
			if drop, ok := ui.DropData(input, opts); ok {
				if err := term.receiveFiles(drop.Data); err != nil {
					a.message = err.Error()
				}
			}
			if a.button(c, "terminal-run", sendIcon, "发送命令") {
				a.submitTerminal()
			}
		})
	})
}
func (a *app) submitTerminal() {
	term := &a.terminal
	line := strings.TrimSpace(term.input)
	if line == "" {
		return
	}
	if !a.terminalOnline() {
		a.message = "设备未连接"
		return
	}
	if term.running {
		if term.shell && term.stdin != nil {
			select {
			case term.stdin <- line + "\n":
				term.rememberCommand(line)
				term.input = ""
				a.appendTerminal("> " + line + "\n")
			default:
				a.message = "输入繁忙，请稍候"
			}
		}
		return
	}
	args, err := parseTerminalArgs(line, term.kind)
	if term.kind == "ADB" && term.mode == 1 {
		args, err = []string{"shell", "-T"}, nil
	}
	if err != nil {
		a.message = err.Error()
		return
	}
	if term.kind == "Fastboot" && riskyFastboot(args) {
		answer, e := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Message: "确认执行 Fastboot 命令？", Detail: "设备：" + term.serial + "\n命令：" + line + "\n此操作可能修改分区或清除数据。", Buttons: []string{"执行", "取消"}, DefaultButton: 1, CancelButton: 1})
		if e != nil || answer.Button != 0 {
			return
		}
		if !a.terminalOnline() {
			a.message = "设备未连接"
			return
		}
	}
	shell := term.kind == "ADB" && term.mode == 1
	term.rememberCommand(line)
	term.input = ""
	term.output += "> " + line + "\n"
	term.running, term.shell = true, shell
	term.generation++
	gen, serial, kind := term.generation, term.serial, term.kind
	ctx, cancel := context.WithCancel(context.Background())
	term.cancel = cancel
	tool := a.engine.adb
	if kind == "Fastboot" {
		tool = a.engine.fastboot
	}
	cmd := a.engine.command(ctx, tool, append([]string{"-s", serial}, args...)...)
	var stdin io.WriteCloser
	if shell {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			term.running = false
			a.message = err.Error()
			return
		}
		term.stdin = make(chan string, 16)
	}
	writer := &terminalWriter{emit: func(s string) {
		a.win.Update(func() {
			if a.terminal.generation == gen {
				a.appendTerminal(s)
			}
		})
	}}
	cmd.Stdout, cmd.Stderr = writer, writer
	if err = cmd.Start(); err != nil {
		cancel()
		term.running = false
		term.stdin = nil
		a.appendTerminal("执行失败\n")
		return
	}
	if a.engine.group != nil {
		if err = a.engine.group.attach(cmd.Process); err != nil {
			cancel()
		}
	}
	if shell {
		input := term.stdin
		input <- line + "\n"
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case text := <-input:
					if _, err := io.WriteString(stdin, text); err != nil {
						return
					}
				}
			}
		}()
	}
	flusherDone := make(chan struct{})
	flusherStopped := make(chan struct{})
	go func() {
		defer close(flusherStopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writer.flush()
			case <-flusherDone:
				writer.flush()
				return
			}
		}
	}()
	go func() {
		err := cmd.Wait()
		cancel()
		close(flusherDone)
		<-flusherStopped
		a.win.Update(func() {
			if a.terminal.generation != gen {
				return
			}
			a.terminal.running, a.terminal.shell = false, false
			a.terminal.stdin, a.terminal.cancel = nil, nil
			if writer.empty() {
				if err != nil {
					a.appendTerminal("执行失败\n")
				} else {
					a.appendTerminal("执行成功\n")
				}
			}
		})
	}()
}

type terminalWriter struct {
	mu      sync.Mutex
	wrote   bool
	pending bytes.Buffer
	emit    func(string)
}

func (w *terminalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > 0 {
		w.wrote = true
		_, _ = w.pending.Write(p)
	}
	return len(p), nil
}
func (w *terminalWriter) flush() {
	w.mu.Lock()
	s := w.pending.String()
	w.pending.Reset()
	w.mu.Unlock()
	if s != "" {
		w.emit(s)
	}
}
func (w *terminalWriter) empty() bool { w.mu.Lock(); defer w.mu.Unlock(); return !w.wrote }
func (a *app) appendTerminal(s string) {
	term := &a.terminal
	follow := term.scroll.MaxY-term.scroll.Y < 4
	term.output += s
	const limit = 1 << 20
	if len(term.output) > limit {
		term.output = term.output[len(term.output)-limit:]
		for !utf8.RuneStart(term.output[0]) {
			term.output = term.output[1:]
		}
	}
	if follow {
		term.scroll.Y = math.MaxFloat32
	}
}
