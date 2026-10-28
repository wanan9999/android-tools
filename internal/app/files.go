package app

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
)

type remoteFile struct {
	name string
	dir  bool
}
type fileState struct {
	serial, directory, message               string
	entries                                  []remoteFile
	loading, busy                            bool
	generation                               uint64
	list                                     ui.ListState
	renameOpen                               bool
	renameOriginal, renameValue, renameError string
}

var folderIcon = icon(`<path d="M3 6a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
var fileIcon = icon(`<path d="M6 3h8l4 4v14H6zM14 3v5h4"/>`)
var renameIcon = icon(`<path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L9 17l-4 1 1-4z"/>`)
var uploadIcon = icon(`<path d="M12 16V3M7 8l5-5 5 5M4 17v3h16v-3"/>`)
var saveFileIcon = icon(`<path d="M12 3v12m-5-5 5 5 5-5M4 17v3h16v-3"/>`)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func validRemoteName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\x00")
}
func (e *engine) listRemote(serial, dir string) ([]remoteFile, error) {
	if !strings.HasPrefix(dir, "/") || strings.ContainsRune(dir, 0) {
		return nil, errors.New("目录无效")
	}
	script := "d=" + shellQuote(dir) + `; [ -d "$d" ] && [ -r "$d" ] || { printf "e\0"; exit; }; n=0; for f in "$d"/* "$d"/.[!.]* "$d"/..?*; do [ -e "$f" ] || [ -L "$f" ] || continue; n=$((n+1)); [ "$n" -le 2000 ] || { printf "x\0"; break; }; if [ -d "$f" ]; then printf 'd%s\0' "${f##*/}"; else printf 'f%s\0' "${f##*/}"; fi; done; printf "z\0"`
	out, err := e.run(e.adb, 15*time.Second, "", "-s", serial, "exec-out", "sh", "-c", script)
	if err != nil {
		return nil, errors.New("无法读取目录，请检查手机连接和访问权限")
	}
	var entries []remoteFile
	complete := false
	for _, item := range strings.Split(out, "\x00") {
		if item == "" {
			continue
		}
		if item == "z" {
			complete = true
			continue
		}
		if item == "e" {
			return nil, errors.New("无法读取目录，请检查访问权限")
		}
		if item == "x" {
			return nil, errors.New("目录文件超过 2000 项，请选择较小的目录")
		}
		if len(item) < 2 || (item[0] != 'd' && item[0] != 'f') || !validRemoteName(item[1:]) {
			return nil, errors.New("目录内容无效")
		}
		entries = append(entries, remoteFile{name: item[1:], dir: item[0] == 'd'})
	}
	if !complete {
		return nil, errors.New("无法读取目录，请检查手机连接和访问权限")
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
	})
	return entries, nil
}
func (a *app) openFiles(serial string) {
	if a.files.busy {
		a.message = "文件传输进行中，请稍候"
		return
	}
	a.files = fileState{serial: serial, directory: "/sdcard", generation: a.files.generation + 1}
	a.page = "files"
	a.loadFiles("/sdcard")
}
func (a *app) loadFiles(dir string) {
	f := &a.files
	if f.busy {
		return
	}
	f.generation++
	gen, serial := f.generation, f.serial
	f.directory = path.Clean(dir)
	f.loading = true
	f.entries = nil
	f.list.ScrollTo(0, ui.Start)
	f.message = ""
	directory := f.directory
	go func() {
		entries, err := a.engine.listRemote(serial, directory)
		a.win.Update(func() {
			if a.page != "files" || a.files.generation != gen {
				return
			}
			a.files.loading = false
			if err != nil {
				a.files.message = err.Error()
			} else {
				a.files.entries = entries
			}
		})
	}()
}
func (a *app) filesView(c *ui.Context) {
	f, t := &a.files, c.Theme()
	ui.Column(c).Grow(1).MinHeight(0).Gap(12).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			if a.button(c, "files-back", backIcon, "返回设备操作") {
				a.openADB(f.serial)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, "文件管理").FontSize(20).Bold()
				ui.Text(c, f.serial).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if a.button(c, "files-up", sendIcon, "上一级") && !f.loading && !f.busy && f.directory != "/" {
				a.loadFiles(path.Dir(f.directory))
			}
			if a.button(c, "files-refresh", refresh, "刷新文件") && !f.busy {
				a.loadFiles(f.directory)
			}
			if a.button(c, "files-upload", uploadIcon, "上传文件") && !f.busy && !f.loading {
				a.chooseUpload()
			}
		})
		ui.Text(c, f.directory).FontSize(12).TextColor(t.TextMuted).SingleLine()
		if f.message != "" {
			ui.Text(c, f.message).FontSize(12).TextColor(t.TextMuted)
		}
		if f.loading {
			ui.Text(c, "正在读取文件…").FontSize(12).TextColor(t.TextMuted)
		}
		if f.busy {
			ui.Text(c, "正在处理，请稍候…").FontSize(12).TextColor(t.TextMuted)
		}
		nextDirectory := ""
		entries := f.entries
		f.list.Key = func(i int) any { return entries[i].name }
		area := ui.List(c, &f.list, len(entries), func(i int) {
			item := entries[i]
			ui.Row(c.Key("file:" + item.name)).AlignItems(ui.Center).Gap(8).Height(40).Children(func() {
				btn := ui.ButtonBase(c.Key("open")).Grow(1).MinWidth(0).Height(38).Padding(0, 10).AlignItems(ui.Center).Justify(ui.Start).Label(item.name).Disabled(f.busy)
				if btn.Hovered() {
					btn.Background(t.SurfaceHover)
				}
				btn.Children(func() {
					ico := fileIcon
					if item.dir {
						ico = folderIcon
					}
					ui.Icon(c, ico).Size(16, 16)
					ui.Text(c, item.name).Margin(0, 0, 0, 10).SingleLine()
				})
				if btn.Clicked() && item.dir && !f.busy {
					nextDirectory = path.Join(f.directory, item.name)
				}
				if !item.dir {
					if a.button(c, "download", saveFileIcon, "下载 "+item.name) && !f.busy {
						a.downloadFile(item.name)
					}
				} else {
					ui.Box(c).Size(34, 34)
				}
				if a.button(c, "rename", renameIcon, "重命名 "+item.name) && !f.busy {
					f.renameOriginal, f.renameValue, f.renameError, f.renameOpen = item.name, item.name, "", true
				}
				if a.button(c, "delete", trash, "删除 "+item.name) && !f.busy {
					a.confirmDelete(item)
				}
			})
		}).Grow(1).MinHeight(0).Radius(10).Background(t.Surface).Padding(8).Label("手机文件列表")
		if drop, ok := ui.DropData(area, transfer.DropOptions{Formats: []transfer.Format{transfer.FileList, transfer.URIList}, Operations: transfer.Copy}); ok && !f.busy && !f.loading {
			paths, err := drop.Data.Files()
			if err != nil || len(paths) == 0 {
				f.message = "无法读取拖入的文件"
			} else {
				a.uploadFiles(paths)
			}
		}
		area.Children(func() {
			if !f.loading && len(f.entries) == 0 && f.message == "" {
				ui.Text(c, "文件夹为空，可拖入电脑文件上传").Padding(14).FontSize(12).TextColor(t.TextMuted)
			}
		})
		if nextDirectory != "" {
			a.loadFiles(nextDirectory)
		}
	})
	ui.Modal(c, &f.renameOpen, func() {
		ui.Column(c).Width(400).Gap(12).Children(func() {
			ui.Text(c, "重命名").FontSize(16).Bold()
			ui.TextInput(c, &f.renameValue).Label("新名称").OnSubmit(func() { a.renameFile() })
			if f.renameError != "" {
				ui.Text(c, f.renameError).FontSize(12).TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					f.renameOpen = false
				}
				if ui.Button(c, "保存").Clicked() {
					a.renameFile()
				}
			})
		})
	})
}
func (e *engine) renameRemote(serial, dir, oldName, newName string) error {
	if !strings.HasPrefix(dir, "/") || !validRemoteName(oldName) || !validRemoteName(newName) {
		return errors.New("文件名无效")
	}
	if oldName == newName {
		return nil
	}
	oldPath, newPath := path.Join(dir, oldName), path.Join(dir, newName)
	command := "if [ ! -e " + shellQuote(oldPath) + " ] && [ ! -L " + shellQuote(oldPath) + " ]; then printf missing; elif [ -e " + shellQuote(newPath) + " ] || [ -L " + shellQuote(newPath) + " ]; then printf exists; elif mv -n -T -- " + shellQuote(oldPath) + " " + shellQuote(newPath) + "; then if [ -e " + shellQuote(oldPath) + " ] || [ -L " + shellQuote(oldPath) + " ]; then printf exists; else printf renamed; fi; else printf failed; fi"
	out, err := e.run(e.adb, 30*time.Second, "", "-s", serial, "exec-out", "sh", "-c", command)
	if err != nil || out == "failed" {
		return errors.New("重命名失败，请检查权限和设备支持")
	}
	switch out {
	case "renamed":
		return nil
	case "exists":
		return errors.New("已有同名文件或文件夹")
	case "missing":
		return errors.New("原文件已不存在")
	default:
		return errors.New("无法确认重命名结果")
	}
}
func (e *engine) deleteRemote(serial, dir, name string) error {
	if !strings.HasPrefix(dir, "/") || !validRemoteName(name) {
		return errors.New("文件名无效")
	}
	target := path.Join(dir, name)
	command := "if [ -e " + shellQuote(target) + " ] || [ -L " + shellQuote(target) + " ]; then if rm -r -- " + shellQuote(target) + "; then printf deleted; else printf failed; fi; else printf missing; fi"
	out, err := e.run(e.adb, 30*time.Minute, "", "-s", serial, "exec-out", "sh", "-c", command)
	if err != nil || out == "failed" {
		return errors.New("删除失败，请检查权限和设备连接")
	}
	switch out {
	case "deleted":
		return nil
	case "missing":
		return errors.New("文件已不存在")
	default:
		return errors.New("无法确认删除结果")
	}
}
func (a *app) renameFile() {
	f := &a.files
	if f.busy {
		return
	}
	name := f.renameValue
	if !validRemoteName(name) {
		f.renameError = "请输入有效的文件名"
		return
	}
	old, dir, serial, gen := f.renameOriginal, f.directory, f.serial, f.generation
	f.renameOpen = false
	if old == name {
		return
	}
	f.busy = true
	go func() {
		err := a.engine.renameRemote(serial, dir, old, name)
		a.finishFileChange(gen, dir, "已重命名", err)
	}()
}
func (a *app) confirmDelete(item remoteFile) {
	f := &a.files
	if f.busy || !validRemoteName(item.name) {
		return
	}
	f.busy = true
	serial, dir, gen := f.serial, f.directory, f.generation
	detail := "设备：" + serial + "\n路径：" + path.Join(dir, item.name) + "\n删除后无法在应用中恢复。"
	if item.dir {
		detail += "\n文件夹中的内容也会被删除。"
	}
	go func() {
		answer, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: a.win, Type: mygo.MessageWarning, Message: "确认删除？", Detail: detail, Buttons: []string{"删除", "取消"}, DefaultButton: 1, CancelButton: 1})
		if err != nil || answer.Button != 0 {
			a.win.Update(func() {
				if a.files.generation == gen {
					a.files.busy = false
				}
			})
			return
		}
		err = a.engine.deleteRemote(serial, dir, item.name)
		a.finishFileChange(gen, dir, "已删除", err)
	}()
}
func (a *app) finishFileChange(gen uint64, dir, success string, err error) {
	a.win.Update(func() {
		if a.files.generation != gen {
			return
		}
		a.files.busy = false
		message := success
		if err != nil {
			message = err.Error()
		}
		if a.page != "files" {
			a.message = message
			return
		}
		if err == nil {
			a.loadFiles(dir)
		}
		a.files.message = message
	})
}

func (a *app) chooseUpload() {
	f := &a.files
	f.busy = true
	gen := f.generation
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择要上传的文件", Multiple: true})
		a.win.Update(func() {
			if a.files.generation != gen {
				return
			}
			a.files.busy = false
			if a.page != "files" {
				return
			}
			if err != nil {
				a.files.message = "无法选择文件"
			} else if len(paths) > 0 {
				a.uploadFiles(paths)
			}
		})
	}()
}
func (a *app) uploadFiles(paths []string) {
	f := &a.files
	if f.busy {
		return
	}
	f.busy = true
	f.message = ""
	serial, dir, gen := f.serial, f.directory, f.generation
	go func() {
		var err error
		count := 0
		for _, local := range paths {
			st, e := os.Stat(local)
			if e != nil || !st.Mode().IsRegular() {
				err = errors.New("仅支持上传普通文件")
				break
			}
			name := filepath.Base(local)
			if !validRemoteName(name) {
				err = errors.New("文件名无效")
				break
			}
			if e = a.engine.pushFile(serial, local, dir, name); e != nil {
				err = e
				break
			}
			count++
		}
		a.win.Update(func() {
			if a.files.generation != gen {
				return
			}
			a.files.busy = false
			if a.page != "files" {
				if err != nil {
					a.message = fmt.Sprintf("已上传 %d 个文件：%s", count, err)
				} else {
					a.message = fmt.Sprintf("已上传 %d 个文件", count)
				}
				return
			}
			if err != nil {
				a.files.message = fmt.Sprintf("已上传 %d 个文件：%s", count, err)
			} else {
				a.loadFiles(dir)
				a.files.message = fmt.Sprintf("已上传 %d 个文件", count)
			}
		})
	}()
}
func (e *engine) pushFile(serial, local, dir, name string) error {
	remote := path.Join(dir, name)
	check := "if [ -e " + shellQuote(remote) + " ] || [ -L " + shellQuote(remote) + " ]; then printf exists; else printf missing; fi"
	out, err := e.run(e.adb, 10*time.Second, "", "-s", serial, "exec-out", "sh", "-c", check)
	if err != nil || (out != "exists" && out != "missing") {
		return errors.New("无法确认手机目标文件")
	}
	if out == "exists" {
		return fmt.Errorf("手机已有同名文件：%s", name)
	}
	nonce, err := randomHex(16)
	if err != nil {
		return err
	}
	stage := path.Join(dir, ".android-tools-"+nonce+".tmp")
	defer func() {
		_, _ = e.run(e.adb, 10*time.Second, "", "-s", serial, "exec-out", "sh", "-c", "rm -f -- "+shellQuote(stage))
	}()
	if _, err = e.run(e.adb, 30*time.Minute, "", "-s", serial, "push", local, stage); err != nil {
		return err
	}
	command := "if mv -n -T -- " + shellQuote(stage) + " " + shellQuote(remote) + "; then if [ -e " + shellQuote(stage) + " ]; then printf exists; else printf moved; fi; else printf failed; fi"
	out, err = e.run(e.adb, 15*time.Second, "", "-s", serial, "exec-out", "sh", "-c", command)
	if err != nil || out == "failed" {
		return errors.New("手机无法安全保存文件，请检查权限或系统支持")
	}
	if out == "exists" {
		return fmt.Errorf("手机已有同名文件：%s", name)
	}
	if out != "moved" {
		return errors.New("无法确认上传结果")
	}
	return nil
}

func (a *app) downloadFile(name string) {
	f := &a.files
	if f.busy || !validRemoteName(name) {
		return
	}
	f.busy = true
	serial, dir, gen := f.serial, f.directory, f.generation
	go func() {
		target, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.win, Title: "保存手机文件", DefaultPath: name})
		if err == nil && target != "" {
			err = a.engine.pullFile(serial, path.Join(dir, name), target)
		}
		a.win.Update(func() {
			if a.files.generation != gen {
				return
			}
			a.files.busy = false
			if a.page != "files" {
				if err != nil {
					a.message = err.Error()
				} else if target != "" {
					a.message = "文件已保存"
				}
				return
			}
			if err != nil {
				a.files.message = err.Error()
			} else if target != "" {
				a.files.message = "文件已保存"
			}
		})
	}()
}
func (e *engine) pullFile(serial, remote, target string) error {
	if _, err := os.Lstat(target); err == nil {
		return errors.New("目标文件已存在，请选择其他名称")
	} else if !os.IsNotExist(err) {
		return err
	}
	dir, err := os.MkdirTemp(filepath.Dir(target), ".android-tools-pull-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	staged := filepath.Join(dir, "file")
	if _, err = e.run(e.adb, 30*time.Minute, "", "-s", serial, "pull", remote, staged); err != nil {
		return err
	}
	src, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	if err = windows.MoveFile(src, dst); err != nil {
		return fmt.Errorf("保存失败，目标文件可能已存在：%w", err)
	}
	return nil
}
