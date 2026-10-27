package app

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func (a *app) openFastboot(serial string) {
	a.fastbootSerial, a.page = serial, "fastboot"
	a.loadFastbootInfo()
}
func (a *app) loadFastbootInfo() {
	a.fastbootInfoRequest++
	request, serial := a.fastbootInfoRequest, a.fastbootSerial
	a.fastbootOutput, a.fastbootInfoError, a.fastbootInfoLoading = "", "", true
	go func() {
		out, err := a.engine.run(a.engine.fastboot, 15*time.Second, "", "-s", serial, "getvar", "all")
		a.win.Update(func() {
			if a.page != "fastboot" || a.fastbootSerial != serial || a.fastbootInfoRequest != request {
				return
			}
			a.fastbootInfoLoading = false
			if err != nil {
				a.fastbootInfoError = "设备信息读取失败，请检查连接后重试"
			} else {
				a.fastbootOutput = out
			}
		})
	}()
}

func (a *app) fastbootView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(600).Gap(14).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			if a.button(c, "back", backIcon, "返回设备") {
				a.backToDevices()
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "Fastboot 操作").FontSize(20).Bold()
				ui.Text(c, a.fastbootSerial).FontSize(12).TextColor(t.TextMuted)
			})
		})
		ui.Column(c).Padding(18).Gap(10).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "设备信息").Bold()
			if a.fastbootInfoLoading {
				ui.Text(c, "正在读取设备信息…").FontSize(12).TextColor(t.TextMuted)
			} else if a.fastbootInfoError != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, a.fastbootInfoError).Grow(1).FontSize(12).TextColor(t.TextMuted)
					if a.button(c, "retry-info", refresh, "重试读取设备信息") {
						a.loadFastbootInfo()
					}
				})
			}
			if a.fastbootOutput != "" {
				ui.Text(c, a.fastbootOutput).FontSize(12).Selectable()
			}
		})
		ui.Column(c).Padding(18).Gap(10).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "启动选项").Bold()
			for _, row := range [][]struct{ label, arg string }{{{"重启系统", ""}, {"重启引导程序", "bootloader"}}, {{"进入 Fastbootd", "fastboot"}, {"进入恢复模式", "recovery"}}} {
				ui.Row(c).Gap(8).Children(func() {
					for _, v := range row {
						v := v
						if ui.Button(c, v.label).Disabled(a.busy).Clicked() {
							serial := a.fastbootSerial
							a.task(func() error {
								args := []string{"-s", serial, "reboot"}
								if v.arg != "" {
									args = append(args, v.arg)
								}
								_, err := a.engine.run(a.engine.fastboot, 20*time.Second, "", args...)
								return err
							})
						}
					}
				})
			}
		})
		ui.Column(c).Padding(18).Gap(10).Radius(10).Background(t.Surface).Children(func() {
			ui.Text(c, "刷写镜像").Bold()
			ui.Text(c, "确认分区、镜像和设备型号匹配；错误操作可能导致设备无法启动。").FontSize(12).TextColor(t.TextMuted)
			ui.TextInput(c, &a.partition).Placeholder("分区名，例如 boot").Label("目标分区")
			if ui.Button(c, "选择镜像并刷写").Disabled(a.busy).Clicked() {
				partition := strings.TrimSpace(a.partition)
				if validPartition(partition) != nil {
					a.message = "请输入有效的分区名"
				} else {
					a.flash(a.fastbootSerial, partition)
				}
			}
		})
	})
}
func (a *app) flash(serial, partition string) {
	a.busy = true
	go func() {
		defer a.win.Update(func() { a.busy = false })
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择要刷写的镜像", Filters: []mygo.FileFilter{{Name: "镜像文件", Extensions: []string{"img"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		path := paths[0]
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			a.win.Update(func() { a.message = "无法读取镜像文件" })
			return
		}
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Message: "确认刷写镜像？", Detail: "设备：" + serial + "\n分区：" + partition + "\n文件：" + path, Buttons: []string{"刷写", "取消"}, DefaultButton: 1, CancelButton: 1})
		if err != nil || answer.Button != 0 {
			return
		}
		a.win.Update(func() { a.flashing = true; a.message = "正在刷写，请勿断开设备或关闭应用" })
		out, err := a.engine.run(a.engine.fastboot, 24*time.Hour, "", "-s", serial, "flash", partition, path)
		a.win.Update(func() {
			a.flashing = false
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = "刷写完成"
				a.fastbootOutput = out
			}
		})
	}()
}
func validPartition(s string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(s) {
		return errors.New("请输入有效的分区名")
	}
	return nil
}
