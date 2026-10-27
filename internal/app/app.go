package app

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"android-tools/internal/appicon"
	"android-tools/internal/tooldeps"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	qrcode "github.com/skip2/go-qrcode"
)

type app struct {
	win                                                *mygo.Window
	engine                                             *engine
	manager                                            *manager
	devices                                            []device
	services                                           []service
	selected, page, address, code, connection, message string
	busy, audio                                        bool
	mirrorSettings                                     bool
	flashing                                           bool
	qrRevision                                         uint64
	qrActive                                           bool
	qrConnecting                                       bool
	qr                                                 *ui.Bitmap
	qrState, pairGUID, partition                       string
	fastbootSerial, fastbootOutput                     string
	fastbootInfoError                                  string
	fastbootInfoLoading                                bool
	fastbootInfoRequest                                uint64
	pairedAddress                                      string
	adbSerial                                          string
	adbInfo                                            identity
	adbDetails                                         deviceDetails
	adbInfoError                                       string
	adbInfoLoading                                     bool
	adbInfoRequest                                     uint64
	size, fps                                          string
	refreshing                                         atomic.Bool
	pendingRefresh                                     atomic.Bool
	deviceRevision                                     atomic.Uint64
	fastbootRevision                                   atomic.Uint64
	pairMode                                           int
	terminal                                           terminalState
}

func icon(s string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + s + `</svg>`))
}

var phone = icon(`<rect x="6" y="2" width="12" height="20" rx="2"/><path d="M10 18h4"/>`)
var refresh = icon(`<path d="M20 7v5h-5M4 17v-5h5"/><path d="M5 9a7 7 0 0 1 12-3l3 3M19 15a7 7 0 0 1-12 3l-3-3"/>`)
var plus = icon(`<path d="M12 5v14M5 12h14"/>`)
var settingsIcon = icon(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-1.8 3.1-.2-.1a1.7 1.7 0 0 0-1.8.7l-.1.2h-3.6l-.1-.2a1.7 1.7 0 0 0-1.8-.7l-.2.1-1.8-3.1.1-.1a1.7 1.7 0 0 0 .3-1.9l-.1-.2-3.2-.1V9.9l.2-.1a1.7 1.7 0 0 0 .7-1.8l-.1-.2 3.1-1.8.1.1a1.7 1.7 0 0 0 1.9.3l.2-.1.1-3.2h3.6l.1.2a1.7 1.7 0 0 0 1.8.7l.2-.1 1.8 3.1-.1.1a1.7 1.7 0 0 0-.3 1.9l.1.2 3.2.1v3.6l-.2.1a1.7 1.7 0 0 0-.7 1.8z"/>`)
var screen = icon(`<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>`)
var link = icon(`<path d="M10 13a5 5 0 0 0 7 .5l3-3a5 5 0 0 0-7-7l-2 2M14 11a5 5 0 0 0-7-.5l-3 3a5 5 0 0 0 7 7l2-2"/>`)
var restart = icon(`<path d="M20 7v5h-5M4 12a8 8 0 1 0 2-5"/>`)
var adbIcon = icon(`<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M12 15h5"/>`)
var fastbootIcon = icon(`<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>`)
var backIcon = icon(`<path d="M19 12H5m0 0 6-6m-6 6 6 6"/>`)
var installIcon = icon(`<rect x="4" y="4" width="16" height="16" rx="2"/><path d="M12 8v8m-4-4h8"/>`)
var cameraIcon = icon(`<path d="M4 7h3l1.5-2h7L17 7h3a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V8a1 1 0 0 1 1-1z"/><circle cx="12" cy="13" r="3"/>`)
var bootIcon = icon(`<rect x="6" y="3" width="12" height="18" rx="2"/><path d="M12 7v8m-3-3 3 3 3-3"/>`)
var recoveryIcon = icon(`<path d="M12 3a9 9 0 1 0 9 9M12 3v5h5"/><path d="M12 9v6m-3-3h6"/>`)
var trash = icon(`<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v5M14 11v5"/>`)
var downloadIcon = icon(`<path d="M12 3v12m-4-4 4 4 4-4M4 17v3h16v-3"/>`)

func (a *app) task(fn func() error) {
	a.taskMessage(fn, "操作完成")
}
func (a *app) taskMessage(fn func() error, success string) {
	if a.busy {
		return
	}
	a.busy = true
	go func() {
		err := fn()
		a.win.Update(func() {
			a.busy = false
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = success
			}
			a.reload()
		})
	}()
}
func (a *app) reload() {
	if a.manager == nil {
		return
	}
	if !a.refreshing.CompareAndSwap(false, true) {
		a.pendingRefresh.Store(true)
		return
	}
	revision := a.deviceRevision.Load()
	fastbootRevision := a.fastbootRevision.Load()
	go func() {
		defer func() {
			a.refreshing.Store(false)
			if a.pendingRefresh.Swap(false) {
				a.reload()
			}
		}()
		ds, ss, err := a.manager.refresh()
		a.win.Update(func() {
			if revision == a.deviceRevision.Load() {
				if fastbootRevision != a.fastbootRevision.Load() {
					ds = mergeFastboot(ds, fastbootDevices(a.devices))
				}
				a.devices = ds
			} else {
				a.pendingRefresh.Store(true)
			}
			if err != nil {
				a.message = err.Error()
			}
			a.services = ss
		})
	}()
}
func (a *app) button(c *ui.Context, key string, ico *ui.SVG, label string) bool {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Size(34, 34).Radius(8).Center().Label(label).Tooltip(label).Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	if b.Hovered() {
		b.Background(t.SurfaceHover)
	}
	b.Children(func() { ui.Icon(c, ico).Size(17, 17) })
	return b.Clicked()
}
func (a *app) view(c *ui.Context) {
	t := c.Theme()
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	bar := c.TitleBar()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		side := ui.Column(c).Width(190).Shrink(0)
		if !c.Vibrancy() {
			side.Background(t.Surface)
		}
		side.Children(func() {
			ui.Row(c).Height(52).Padding(0, 16).AlignItems(ui.Center).DragWindow().Children(func() {
				ui.Icon(c, phone).Size(19, 19)
				ui.Text(c, "android-tools").FontSize(15).Bold().Margin(0, 0, 0, 10)
			})
			sidebar := ui.Sidebar(c, &a.page, func() {
				ui.SidebarSection(c, "设备", nil, func() {
					ui.SidebarItem(c, "devices", phone, "所有设备")
					ui.SidebarItem(c, "pair", link, "无线连接")
				})
			}).Grow(1)
			if c.Vibrancy() {
				sidebar.Background(ui.Transparent)
			}
		})
		if a.page != "terminal" && a.terminal.running {
			a.stopTerminal()
		}
		ui.Box(c).Width(1).Background(t.Border)
		ui.Column(c).Grow(1).MinWidth(0).Background(t.Background).Children(func() {
			ui.Row(c).Height(52).Shrink(0).Padding(0, bar.Right+16, 0, 20).AlignItems(ui.Center).DragWindow().Children(func() {
				title := "所有设备"
				if a.page == "pair" {
					title = "无线连接"
				} else if a.page == "fastboot" {
					title = "Fastboot"
				} else if a.page == "terminal" {
					title = "终端"
				} else if a.page == "adb" {
					title = "ADB 设备"
				}
				ui.Text(c, title).FontSize(15).Bold()
				ui.Box(c).Grow(1)
			})
			ui.Divider(c)
			if a.page == "terminal" {
				ui.Column(c).Grow(1).MinHeight(0).Padding(22, 24).Gap(12).Children(func() {
					if a.message != "" {
						ui.Text(c, a.message).FontSize(12).TextColor(t.TextMuted)
					}
					a.terminalView(c)
				})
			} else {
				ui.Scroll(c).Grow(1).Children(func() {
					ui.Column(c).Padding(22, 24).Gap(16).Children(func() {
						if a.message != "" {
							ui.Text(c, a.message).FontSize(12).TextColor(t.TextMuted)
						}
						if a.page == "pair" {
							a.pairView(c)
						} else if a.page == "fastboot" {
							a.fastbootView(c)
						} else if a.page == "adb" {
							a.adbView(c)
						} else {
							a.deviceView(c)
						}
					})
				})
			}
		})
	})
}
func (a *app) deviceView(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "设备").FontSize(20).Bold()
			ui.Text(c, "管理 USB、无线调试和 Fastboot 设备").FontSize(12).TextColor(t.TextMuted)
		})
		settings := ui.ButtonBase(c.Key("mirror-settings")).Size(34, 34).Radius(8).Center().Label("投屏设置").Tooltip("投屏设置").Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
		if settings.Hovered() {
			settings.Background(t.SurfaceHover)
		}
		settings.Children(func() { ui.Icon(c, settingsIcon).Size(17, 17) })
		if settings.Clicked() {
			a.mirrorSettings = !a.mirrorSettings
		}
		ui.Popover(c, settings, &a.mirrorSettings, func() {
			ui.Column(c).Width(220).Padding(10).Gap(12).Children(func() {
				ui.Text(c, "投屏设置").Bold()
				ui.Checkbox(c, &a.audio, "转发声音")
				ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "画面").Grow(1).FontSize(12).TextColor(t.TextMuted)
					ui.Select(c, &a.size, []string{"720", "1280", "1920", "2560"}).Width(105).Label("分辨率上限")
				})
				ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, "帧率").Grow(1).FontSize(12).TextColor(t.TextMuted)
					ui.Select(c, &a.fps, []string{"15", "30", "60"}).Width(105).Label("帧率")
				})
				ui.Text(c, "下次启动投屏时生效").FontSize(12).TextColor(t.TextMuted)
			})
		})
		if a.button(c, "add", plus, "无线连接") {
			a.page = "pair"
		}
		if a.button(c, "tools", downloadIcon, "检查并下载组件") {
			a.taskMessage(func() error {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				_, err = tooldeps.Ensure(filepath.Dir(exe))
				if err != nil {
					return err
				}
				return a.engine.start()
			}, "组件检查完成")
		}
		if a.button(c, "refresh", refresh, "刷新设备") {
			a.reload()
		}
	})

	if len(a.devices) == 0 {
		ui.Column(c).Padding(30).Gap(8).Radius(12).Background(t.Surface).Children(func() {
			ui.Text(c, "尚未发现设备").Bold()
			ui.Text(c, "通过 USB 连接并允许调试，或添加无线设备。").FontSize(12).TextColor(t.TextMuted)
		})
	}
	for _, item := range a.devices {
		d := item
		ui.Row(c.Key(d.Kind+":"+d.Serial)).Padding(14, 16).Gap(12).Radius(10).Background(t.Surface).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, phone).Size(20, 20).TextColor(t.TextMuted)
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				name := d.Model
				if name == "" {
					name = "Android 设备"
				}
				ui.Text(c, strings.ReplaceAll(name, "_", " ")).Bold()
				state := map[string]string{"device": "已连接", "offline": "离线", "unauthorized": "等待授权", "identity_error": "身份无法核验", "verifying": "正在核验", "fastboot": "已连接", "no permissions": "权限不足"}[d.State]
				if state == "" {
					state = d.State
				}
				ui.Text(c, d.Serial+"  ·  "+d.Kind+"  ·  "+state).FontSize(12).TextColor(t.TextMuted)
				if reason := a.engine.mirrorError(d.Serial); reason != "" {
					ui.Text(c, reason).FontSize(12).TextColor(t.TextMuted)
				}
			})
			if d.Kind == "ADB" && d.State == "device" {
				if a.button(c, "manage", adbIcon, "ADB 操作") {
					a.openADB(d.Serial)
				}
				mirroring := a.engine.isMirroring(d.Serial)
				label := "打开手机画面"
				if mirroring {
					label = "关闭手机画面"
				}
				if a.button(c, "screen", screen, label) {
					audio, size, fps := a.audio, a.size, a.fps
					a.task(func() error {
						if mirroring {
							return a.engine.stopMirror(d.Serial)
						}
						return a.engine.mirror(d.Serial, audio, size, fps)
					})
				}
			}
			if d.Kind == "Fastboot" {
				if a.button(c, "manage", fastbootIcon, "Fastboot 操作") {
					a.openFastboot(d.Serial)
				}
				if a.button(c, "reboot", restart, "重启设备") {
					a.task(func() error {
						_, err := a.engine.run(a.engine.fastboot, 15*time.Second, "", "-s", d.Serial, "reboot")
						return err
					})
				}
			}
			if d.State == "offline" && d.Hardware != "" && a.manager != nil && a.button(c, "forget", trash, "移除离线设备记录") {
				a.task(func() error { return a.manager.forget(d.Hardware) })
			}
		})
	}
}
func (a *app) pairView(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).MaxWidth(560).Gap(12).Children(func() {
		ui.Text(c, "连接无线调试").FontSize(20).Bold()
		ui.Text(c, "确保手机与电脑连接同一网络，并在手机开启无线调试。").FontSize(12).TextColor(t.TextMuted)
		previousMode := a.pairMode
		ui.Segmented(c, &a.pairMode, "扫码配对", "配对码", "已配对").Label("连接方式")
		if previousMode != a.pairMode && a.qrActive && a.manager != nil {
			a.qrRevision++
			a.manager.cancelPair()
			a.qr = nil
			a.qrActive = false
			a.qrConnecting = false
			a.qrState = ""
		}
		if previousMode != a.pairMode {
			a.pairGUID = ""
			a.pairedAddress = ""
			a.connection = ""
			a.selected = ""
		}
		if a.pairMode == 0 {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, "使用二维码配对").Bold()
				ui.Text(c, "在手机无线调试页面选择“使用二维码配对”。二维码 3 分钟内有效。").FontSize(12).TextColor(t.TextMuted)
				if a.qr != nil {
					ui.Image(c, a.qr).Size(180, 180)
				}
				if a.qrState != "" {
					ui.Text(c, a.qrState).FontSize(12).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(8).Children(func() {
					if !a.qrActive && ui.Button(c, "生成二维码").Disabled(a.busy).Clicked() && a.manager != nil {
						a.qrRevision++
						revision := a.qrRevision
						payload, err := a.manager.beginQR(func(state, message string) {
							a.win.Update(func() {
								if revision != a.qrRevision {
									return
								}
								a.qrState = message
								a.qrConnecting = state == "connecting"
								if state != "waiting" {
									a.qr = nil
								}
								if state == "connected" || state == "error" || state == "expired" {
									a.qrActive = false
								}
								if state == "connected" {
									a.reload()
								}
							})
						})
						if err == nil {
							var data []byte
							data, err = qrcode.Encode(payload, qrcode.Medium, 256)
							if err == nil {
								a.qr, err = ui.DecodeBitmap(data)
							}
						}
						if err != nil {
							a.message = err.Error()
							a.qrRevision++
							a.manager.cancelPair()
						} else {
							a.qrState = "等待手机扫码"
							a.qrActive = true
						}
					}
					if a.qrActive && ui.Button(c, "取消").Clicked() {
						a.qrRevision++
						a.manager.cancelPair()
						a.qr = nil
						a.qrActive = false
						a.qrConnecting = false
						a.qrState = "已取消"
					}
				})
				if a.qrConnecting {
					ui.Text(c, "若未自动连接，请在手机返回无线调试首页，填写显示的 IP 和连接端口。").FontSize(12).TextColor(t.TextMuted)
					ui.TextInput(c, &a.connection).Placeholder("连接 IP:端口").Label("连接地址")
					if ui.PrimaryButton(c, "连接手机").Clicked() && a.manager != nil {
						if err := a.manager.submitQRAddress(strings.TrimSpace(a.connection)); err != nil {
							a.qrState = err.Error()
						} else {
							a.qrState = "正在核验并连接手机"
						}
					}
				}
			})
		}
		if a.pairMode == 1 {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, "1  建立配对").Bold()
				ui.Text(c, "在手机的无线调试页面打开“使用配对码配对”。").FontSize(12).TextColor(t.TextMuted)
				ui.TextInput(c, &a.address).Placeholder("配对 IP:端口").Label("配对地址")
				ui.TextInput(c, &a.code).Placeholder("六位配对码").Label("配对码")
				if ui.PrimaryButton(c, "配对").Disabled(a.busy).Clicked() {
					if a.manager != nil {
						a.manager.cancelPair()
						a.qr = nil
						a.qrActive = false
					}
					a.pairGUID = ""
					a.pairedAddress = ""
					addr, code := strings.TrimSpace(a.address), a.code
					a.code = ""
					a.taskMessage(func() error {
						if !regexp.MustCompile(`^[0-9]{6}$`).MatchString(code) {
							return errors.New("请输入六位配对码")
						}
						guid, err := a.engine.pairSecret(addr, code)
						if err == nil {
							a.win.Update(func() {
								a.pairGUID = guid
								a.pairedAddress = addr
								a.message = "配对成功，请输入无线调试首页的连接地址"
							})
						}
						return err
					}, "配对成功，请输入无线调试首页的连接地址")
				}
			})
		}
		if a.pairMode != 0 {
			ui.Column(c).Gap(10).Children(func() {
				title := "连接已配对设备"
				if a.pairMode == 1 {
					title = "2  连接设备"
				}
				ui.Text(c, title).Bold()
				selectedHardware := ""
				if a.pairMode == 2 && a.manager != nil {
					options := []string{"选择已保存设备（可选）"}
					byLabel := map[string]savedDevice{}
					for _, d := range a.manager.registry.snapshot() {
						if d.GUID == "" {
							continue
						}
						name := d.Model
						if name == "" {
							name = "Android 设备"
						}
						label := name + " · " + d.Hardware
						options = append(options, label)
						byLabel[label] = d
					}
					if len(options) > 1 {
						if a.selected == "" {
							a.selected = options[0]
						}
						before := a.selected
						ui.Select(c, &a.selected, options).Label("已保存设备")
						if d, ok := byLabel[a.selected]; ok {
							selectedHardware = d.Hardware
							if before != a.selected {
								a.connection = d.Address
							}
						}
					}
				}
				ui.Text(c, "返回无线调试首页，输入显示的 IP 和连接端口。").FontSize(12).TextColor(t.TextMuted)
				ui.TextInput(c, &a.connection).Placeholder("连接 IP:端口").Label("连接地址")
				if ui.PrimaryButton(c, "连接").Disabled(a.busy).Clicked() {
					if a.manager != nil {
						a.manager.cancelPair()
						a.qr = nil
						a.qrActive = false
					}
					addr := strings.TrimSpace(a.connection)
					guid := a.pairGUID
					pairAddress := a.pairedAddress
					hardware := selectedHardware
					a.task(func() error {
						if err := validConnectionAddress(pairAddress, addr); err != nil {
							return err
						}
						if a.manager == nil {
							return errors.New("设备记录不可用")
						}
						if hardware != "" {
							return a.manager.connectKnown(addr, hardware)
						}
						return a.manager.connect(addr, guid)
					})
				}
			})
		}
		ui.Text(c, "提示：配对端口与连接端口不同；无线配对需要 Android 11 或以上。").FontSize(12).TextColor(t.TextMuted)
	})
}
func Run() {
	a := &app{engine: newEngine(), page: "devices", audio: true, size: "1280", fps: "30"}
	group, groupErr := newProcessGroup()
	if groupErr == nil {
		a.engine.group = group
	} else {
		a.engine.adb = ""
		a.message = "无法初始化进程保护：" + groupErr.Error()
	}
	stopping := make(chan struct{})
	watchCtx, stopWatch := context.WithCancel(context.Background())
	dir, err := os.UserConfigDir()
	if err == nil {
		var reg *registry
		reg, err = openRegistry(filepath.Join(dir, "android-tools", "devices.json"))
		if err == nil {
			a.manager = newManager(a.engine, reg)
		}
	}
	if err != nil {
		a.message = "设备记录不可用：" + err.Error()
	}
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{Title: "android-tools", Width: 960, Height: 680, MinWidth: 640, MinHeight: 480, TitleBarStyle: mygo.TitleBarHiddenInset, TitleBarHeight: 52, Vibrancy: mygo.VibrancyMica, Content: ui.View(a.view)})
		if iconPNG, err := appicon.PNG(256); err == nil {
			_ = a.win.SetIcon(iconPNG)
		}
		a.win.OnClose(func(e *mygo.CloseEvent) {
			if a.flashing {
				e.PreventDefault()
				a.message = "正在刷写，请等待操作结束后退出"
				a.win.Invalidate()
			}
		})
		go a.engine.trackADB(watchCtx, func(ds []device) {
			a.deviceRevision.Add(1)
			a.win.Update(func() { a.devices = mergeADBEvent(a.devices, ds); a.checkTerminalDevice(); a.reload() })
		})
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			var last []device
			haveLast := false
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
				}
				ds, err := a.engine.listFastboot()
				if err != nil {
					continue
				}
				if haveLast && sameFastboot(last, ds) {
					continue
				}
				last = append([]device(nil), ds...)
				haveLast = true
				a.win.Update(func() {
					if !sameFastboot(a.devices, ds) {
						a.fastbootRevision.Add(1)
						a.devices = mergeFastboot(a.devices, ds)
						a.checkTerminalDevice()
					}
				})
			}
		}()
		go func() {
			err := a.engine.start()
			a.win.Update(func() {
				if err != nil {
					a.message = err.Error()
				}
				if a.manager != nil {
					a.reload()
				}
			})
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-stopping:
					return
				case <-ticker.C:
				}
				if err := a.engine.start(); err != nil {
					a.win.Update(func() { a.message = err.Error() })
				}
				a.reload()
			}
		}()
	})
	if err := mygo.App.Run(); err != nil {
		log.Print(err)
	}
	a.stopTerminal()
	close(stopping)
	stopWatch()
	if a.manager != nil {
		a.manager.cancelPair()
	}
	a.engine.stop()
	group.close()
}
