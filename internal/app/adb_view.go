package app

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) openADB(serial string) {
	a.adbSerial, a.page = serial, "adb"
	a.loadADBInfo()
}

func (a *app) loadADBInfo() {
	a.adbInfoRequest++
	request, serial := a.adbInfoRequest, a.adbSerial
	a.adbInfo, a.adbInfoError, a.adbInfoLoading = identity{}, "", true
	a.adbDetails = deviceDetails{}
	go func() {
		id, err := a.engine.identify(serial)
		a.win.Update(func() { a.applyADBInfo(request, serial, id, err) })
		if err == nil {
			details := a.engine.loadDeviceDetails(serial)
			a.win.Update(func() {
				if a.page == "adb" && a.adbSerial == serial && a.adbInfoRequest == request {
					a.adbDetails = details
				}
			})
		}
	}()
}

type deviceDetails struct{ Resolution, Battery string }

var screenSizePattern = regexp.MustCompile(`(?m)(?:Physical|Override) size:\s*(\d+\s*x\s*\d+)`)
var batteryLevelPattern = regexp.MustCompile(`(?m)^\s*level:\s*(\d+)\s*$`)

func (e *engine) loadDeviceDetails(serial string) deviceDetails {
	var details deviceDetails
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out, err := e.run(e.adb, 3*time.Second, "", "-s", serial, "shell", "wm", "size")
		if err == nil {
			matches := screenSizePattern.FindAllStringSubmatch(out, -1)
			if len(matches) > 0 {
				details.Resolution = strings.ReplaceAll(matches[len(matches)-1][1], " ", "")
			}
		}
	}()
	go func() {
		defer wg.Done()
		out, err := e.run(e.adb, 3*time.Second, "", "-s", serial, "shell", "dumpsys", "battery")
		if err == nil {
			m := batteryLevelPattern.FindStringSubmatch(out)
			if len(m) > 1 {
				level := 0
				fmt.Sscan(m[1], &level)
				if level >= 0 && level <= 100 {
					details.Battery = fmt.Sprintf("%d%%", level)
				}
			}
		}
	}()
	wg.Wait()
	return details
}

func (a *app) applyADBInfo(request uint64, serial string, id identity, err error) {
	if a.page != "adb" || a.adbSerial != serial || a.adbInfoRequest != request {
		return
	}
	a.adbInfoLoading = false
	if err != nil {
		a.adbInfoError = "设备信息读取失败，请检查连接后重试"
		return
	}
	a.adbInfo, a.adbInfoError = id, ""
}

func (a *app) backToDevices() { a.adbInfoRequest++; a.fastbootInfoRequest++; a.page = "devices" }

func (a *app) adbAction(c *ui.Context, label string, symbol *ui.SVG) bool {
	button := ui.Button(c, "").Grow(1).Height(40).Label(label).Disabled(a.busy)
	button.Children(func() { ui.Icon(c, symbol).Size(16, 16); ui.Text(c, label).Margin(0, 0, 0, 8).SingleLine() })
	return button.Clicked()
}

func (a *app) adbView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(600).Gap(16).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if a.button(c, "back", backIcon, "返回设备") {
				a.backToDevices()
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, "ADB 操作").FontSize(20).Bold()
				ui.Text(c, a.adbSerial).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		})
		ui.Column(c).Padding(20).Gap(12).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "设备信息").Bold()
			if a.adbInfoLoading {
				ui.Text(c, "正在读取设备信息…").FontSize(12).TextColor(t.TextMuted)
			} else if a.adbInfoError != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.adbInfoError).FontSize(12).TextColor(t.TextMuted).Grow(1)
					if a.button(c, "retry-info", refresh, "重试读取设备信息") {
						a.loadADBInfo()
					}
				})
			} else if a.adbInfo.Hardware != "" {
				model := a.adbInfo.Model
				if model == "" {
					model = "未知"
				}
				sdk := "未知"
				if a.adbInfo.SDK > 0 {
					sdk = fmt.Sprint(a.adbInfo.SDK)
				}
				for _, row := range []struct{ label, value string }{{"设备型号", model}, {"品牌", a.adbInfo.Brand}, {"制造商", a.adbInfo.Manufacturer}, {"设备代号", a.adbInfo.Device}, {"Android 版本", a.adbInfo.Android}, {"Android SDK", sdk}, {"安全补丁", a.adbInfo.SecurityPatch}, {"系统版本", a.adbInfo.BuildID}, {"处理器架构", a.adbInfo.ABI}, {"屏幕分辨率", a.adbDetails.Resolution}, {"电量", a.adbDetails.Battery}, {"硬件身份", a.adbInfo.Hardware}} {
					row := row
					if strings.TrimSpace(row.value) == "" {
						row.value = "未提供"
					}
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, row.label).Width(100).Shrink(0).FontSize(12).TextColor(t.TextMuted)
						ui.Text(c, row.value).Grow(1).MinWidth(0).FontSize(13).Selectable()
					})
				}
			}
		})
		ui.Column(c).Padding(20).Gap(12).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "文件与画面").Bold()
			ui.Grid(c).Columns(2).Gap(8).Children(func() {
				if a.adbAction(c, "安装 APK", installIcon) {
					a.installAPK(a.adbSerial)
				}
				if a.adbAction(c, "保存截图", cameraIcon) {
					a.screenshot(a.adbSerial)
				}
			})
		})
		ui.Column(c).Padding(20).Gap(12).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "电源与启动").Bold()
			ui.Grid(c).Columns(2).Gap(8).Children(func() {
				for _, v := range []struct {
					label, arg string
					icon       *ui.SVG
				}{{"重启系统", "", restart}, {"进入引导程序", "bootloader", bootIcon}, {"进入恢复模式", "recovery", recoveryIcon}} {
					if a.adbAction(c, v.label, v.icon) {
						serial := a.adbSerial
						a.task(func() error {
							args := []string{"-s", serial, "reboot"}
							if v.arg != "" {
								args = append(args, v.arg)
							}
							_, err := a.engine.run(a.engine.adb, 20*time.Second, "", args...)
							return err
						})
					}
				}
			})
		})
	})
}
func (a *app) installAPK(serial string) {
	a.busy = true
	go func() {
		defer a.win.Update(func() { a.busy = false })
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择 APK", Filters: []mygo.FileFilter{{Name: "Android 安装包", Extensions: []string{"apk"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		p := paths[0]
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(p), ".apk") {
			a.win.Update(func() { a.message = "请选择有效的 APK 文件" })
			return
		}
		out, err := a.engine.run(a.engine.adb, 5*time.Minute, "", "-s", serial, "install", p)
		a.win.Update(func() {
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = out
			}
		})
	}()
}
func (e *engine) capture(serial, path string) error {
	if e.adb == "" {
		return errors.New("未找到 adb.exe")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".screenshot-*.png")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := e.command(ctx, e.adb, "-s", serial, "exec-out", "screencap", "-p")
	cmd.Stdout = f
	cmd.Stderr = io.Discard
	err = cmd.Run()
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("截图失败，请检查设备连接")
	}
	st, err := os.Stat(name)
	if err != nil || st.Size() < 8 || st.Size() > 50<<20 {
		return errors.New("截图数据无效")
	}
	check, err := os.Open(name)
	if err != nil {
		return err
	}
	config, err := png.DecodeConfig(check)
	_ = check.Close()
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return errors.New("截图数据无效")
	}
	return os.Rename(name, path)
}
func (a *app) screenshot(serial string) {
	a.busy = true
	go func() {
		defer a.win.Update(func() { a.busy = false })
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "保存截图", DefaultPath: "screenshot.png", Filters: []mygo.FileFilter{{Name: "PNG 图片", Extensions: []string{"png"}}}})
		if err != nil || path == "" {
			return
		}
		err = a.engine.capture(serial, path)
		a.win.Update(func() {
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = "截图已保存"
			}
		})
	}()
}
