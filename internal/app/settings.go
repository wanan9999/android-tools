package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
		ui.Text(c, "网络代理").Bold()
		ui.Column(c).Padding(16).Gap(12).Radius(10).Background(t.Surface).Children(func() {
			if a.proxyProtocol == "" {
				a.proxyProtocol = "HTTP"
			}
			ui.Checkbox(c, &a.proxySystem, "使用系统代理").OnChange(a.networkChanged)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Select(c, &a.proxyProtocol, []string{"HTTP", "SOCKS5"}).Width(100).Label("代理协议").Disabled(a.proxySystem).OnChange(a.networkChanged)
				ui.TextInput(c, &a.proxyAddress).Grow(1).MinWidth(0).Placeholder("IP:端口，留空直连").Label("代理地址").Disabled(a.proxySystem).OnChange(a.networkChanged)
				if a.button(c, "proxy-test", boltIcon, "测试延迟") && !a.proxyTesting {
					a.testProxy()
				}
				if a.proxyLatency != "" {
					color := t.TextMuted
					if a.proxyLatency == "-1" {
						color = t.Danger
					}
					ui.Text(c, a.proxyLatency).Width(58).FontSize(12).TextColor(color).Tooltip(a.proxyError)
				}
			})
		})
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
		if a.toolsDownloading {
			downloadProgressView(c, a.toolsProgress)
			if a.button(c, "tools-cancel", closeIcon, "取消下载") && a.toolsCancel != nil {
				a.toolsCancel()
			}
		}
	})
}

func (a *app) networkChanged() {
	a.proxyRevision++
	a.proxyLatency = ""
	a.proxyError = ""
	a.proxyTesting = false
	a.savePreferences()
}
func (a *app) networkDownloadOptions(progress func(tooldeps.Progress)) (tooldeps.DownloadOptions, error) {
	cfg := tooldeps.NetworkConfig{Protocol: a.proxyProtocol, Address: strings.TrimSpace(a.proxyAddress), System: a.proxySystem}
	client, err := cfg.Client()
	if err != nil {
		return tooldeps.DownloadOptions{}, err
	}
	client.Timeout = 2 * time.Minute
	return tooldeps.DownloadOptions{Client: client, Progress: progress}, nil
}
func (a *app) testProxy() {
	a.proxyRevision++
	revision := a.proxyRevision
	options, err := a.networkDownloadOptions(nil)
	if err != nil {
		a.proxyLatency = "-1"
		a.proxyError = err.Error()
		return
	}
	a.proxyTesting = true
	a.proxyLatency = "…"
	a.proxyError = "正在测试"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		client := *options.Client
		defer client.CloseIdleConnections()
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		elapsed, err := measureProxyLatency(ctx, &client, "https://www.google.com/generate_204")
		a.win.Update(func() {
			if revision != a.proxyRevision {
				return
			}
			a.proxyTesting = false
			if err != nil {
				a.proxyLatency = "-1"
				a.proxyError = "连接失败或超时"
			} else {
				a.proxyLatency = fmt.Sprintf("%d ms", elapsed.Milliseconds())
				a.proxyError = "已连接 · Google 204 响应延迟"
			}
		})
	}()
}

// measureProxyLatency warms the connection, then measures a subsequent request.
func measureProxyLatency(ctx context.Context, client *http.Client, target string) (time.Duration, error) {
	request := func() (time.Duration, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return 0, err
		}
		start := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(start)
		if err != nil {
			return 0, err
		}
		closeErr := resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return 0, fmt.Errorf("测试地址返回状态 %d", resp.StatusCode)
		}
		return elapsed, closeErr
	}
	if _, err := request(); err != nil {
		return 0, err
	}
	elapsed, err := request()
	if err != nil && ctx.Err() == nil {
		return request()
	}
	return elapsed, err
}
func downloadProgressView(c *ui.Context, p tooldeps.Progress) {
	if p.Name == "" {
		ui.Text(c, "准备下载…").FontSize(12).TextColor(c.Theme().TextMuted)
		return
	}
	value := -1.0
	label := fmt.Sprintf("%s · %.1f MB", p.Name, float64(p.Current)/(1024*1024))
	if p.Total > 0 {
		value = float64(p.Current) / float64(p.Total)
		if value > 1 {
			value = 1
		}
		label = fmt.Sprintf("%s · %.1f / %.1f MB", p.Name, float64(p.Current)/(1024*1024), float64(p.Total)/(1024*1024))
	}
	ui.Column(c).Gap(6).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Progress(c, value).Label("下载进度")
	})
}

func (a *app) prepareTools() {
	if a.busy || a.toolsDownloading {
		return
	}
	options, err := a.networkDownloadOptions(func(p tooldeps.Progress) {
		a.win.Update(func() {
			if a.toolsDownloading {
				a.toolsProgress = p
			}
		})
	})
	if err != nil {
		a.message = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	options.Context = ctx
	a.busy = true
	a.toolsDownloading = true
	a.toolsProgress = tooldeps.Progress{}
	a.toolsCancel = cancel
	go func() {
		defer cancel()
		defer options.Client.CloseIdleConnections()
		exe, err := os.Executable()
		if err == nil {
			_, err = tooldeps.Ensure(filepath.Dir(exe), options)
		}
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			err = a.engine.start()
		}
		if err == nil {
			select {
			case a.fastbootWake <- struct{}{}:
			default:
			}
		}
		cancelled := errors.Is(err, context.Canceled)
		a.win.Update(func() {
			a.busy = false
			a.toolsDownloading = false
			a.toolsCancel = nil
			if err != nil {
				if cancelled {
					a.message = "已取消下载"
				} else {
					a.message = err.Error()
				}
			} else {
				a.message = "组件检查完成"
			}
			a.reload()
		})
	}()
}
