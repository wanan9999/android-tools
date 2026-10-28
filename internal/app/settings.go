package app

import (
	"os"
	"path/filepath"

	"android-tools/internal/tooldeps"
	"github.com/egoist/mygo/ui"
)

// settingsView contains defaults shared by all devices; changes apply on the next launch.
func (a *app) settingsView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(600).Gap(16).Children(func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "应用设置").FontSize(20).Bold()
		})
		ui.Text(c, "投屏").Bold()
		ui.Column(c).Padding(16).Gap(16).Radius(10).Background(t.Surface).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, "声音转发").Bold()
					ui.Text(c, "将手机声音传到电脑").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Checkbox(c, &a.audio, "转发声音").OnChange(a.savePreferences)
			})
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, "画面尺寸").Bold()
					ui.Text(c, "限制画面最长边的像素").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Select(c, &a.size, []string{"720", "1280", "1920", "2560"}).Width(112).Label("分辨率上限").OnChange(a.savePreferences)
			})
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, "画面帧率").Bold()
					ui.Text(c, "限制每秒画面的帧数").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Select(c, &a.fps, []string{"15", "30", "60"}).Width(112).Label("帧率").OnChange(a.savePreferences)
			})
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, "视频码率").Bold()
					ui.Text(c, "限制视频传输速率").FontSize(12).TextColor(t.TextMuted)
				})
				ui.Select(c, &a.bitrate, []string{"8 Mbps", "12 Mbps", "16 Mbps", "24 Mbps"}).Width(112).Label("视频码率").OnChange(a.savePreferences)
			})
		})
		ui.Text(c, "更改在下次投屏时生效").FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, "组件").Bold()
		ui.Row(c).Padding(16).Gap(16).Radius(10).Background(t.Surface).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "工具组件").Bold()
				ui.Text(c, "检查并下载缺失组件").FontSize(12).TextColor(t.TextMuted)
			})
			if a.button(c, "tools", refresh, "检查并下载组件") {
				a.prepareTools()
			}
		})
	})
}

func (a *app) prepareTools() {
	a.taskMessage(func() error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if _, err = tooldeps.Ensure(filepath.Dir(exe)); err != nil {
			return err
		}
		if err = a.engine.start(); err != nil {
			return err
		}
		select {
		case a.fastbootWake <- struct{}{}:
		default:
		}
		return nil
	}, "组件检查完成")
}
