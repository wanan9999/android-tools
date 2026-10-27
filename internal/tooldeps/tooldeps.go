// Package tooldeps prepares the official, digest-pinned Android tools.
package tooldeps

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Directory = "android-tools"
const manifestName = "manifest.json"

var trustedManifestSHA = "a4761574ab15ca76420f38fcd981dfca3d90fdfc333dbfc965cf584cbb57b5b2"

type source struct {
	url, sha string
	allowed  map[string]bool
}

var sources = []source{
	{"https://dl.google.com/android/repository/platform-tools-latest-windows.zip", "45f4d63113e895ebde0c90f194099a4676b6ac653bd28d54314a9e022bbc1a99", map[string]bool{"adb.exe": true, "fastboot.exe": true, "AdbWinApi.dll": true, "AdbWinUsbApi.dll": true, "libwinpthread-1.dll": true, "NOTICE.txt": true, "source.properties": true}},
	{"https://github.com/Genymobile/scrcpy/releases/download/v5.0.1/scrcpy-win64-v5.0.1.zip", "b12a2c4ee8be317422451fc7dcf8ee20a71b5ea7ef9ad73ddd825a25316227a5", nil},
}
var required = []string{"adb.exe", "fastboot.exe", "AdbWinApi.dll", "AdbWinUsbApi.dll", "scrcpy.exe", "scrcpy-server", "SDL3.dll", "avcodec-63.dll", "avformat-63.dll", "avutil-61.dll", "swresample-7.dll", "libusb-1.0.dll", "NOTICE.txt", "LICENSE.txt"}

type manifest struct {
	Files map[string]string `json:"files"`
}

func digest(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func load(dir string) (manifest, error) {
	var m manifest
	b, e := os.ReadFile(filepath.Join(dir, manifestName))
	if e != nil {
		return m, e
	}
	if len(b) > 128<<10 {
		return m, errors.New("组件清单过大")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != trustedManifestSHA {
		return m, errors.New("组件清单与发行版不一致")
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	for _, n := range required {
		if len(m.Files[n]) != 64 {
			return m, errors.New("组件清单不完整")
		}
	}
	return m, nil
}

// Check validates every recorded component and distinguishes missing from modified files.
func Check(dir string) error {
	m, e := load(dir)
	if e != nil {
		return errors.New("组件缺失或清单不可用")
	}
	for name, want := range m.Files {
		if !safeName(name) {
			return errors.New("组件清单包含无效文件名")
		}
		got, e := digest(filepath.Join(dir, name))
		if e != nil {
			return errors.New("组件缺失")
		}
		if got != want {
			return errors.New("组件已损坏或被修改")
		}
	}
	return nil
}
func safeName(n string) bool {
	return n != "" && n != "." && n != ".." && filepath.Base(n) == n && !strings.ContainsAny(n, `\/:`)
}
func download(s source, dir string) (string, error) {
	resp, e := (&http.Client{Timeout: 2 * time.Minute}).Get(s.url)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载失败：%s", resp.Status)
	}
	f, e := os.CreateTemp(dir, "download-*.zip")
	if e != nil {
		return "", e
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, (200<<20)+1))
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return "", e
	}
	if n > 200<<20 || hex.EncodeToString(h.Sum(nil)) != s.sha {
		return "", errors.New("下载组件摘要不匹配")
	}
	return f.Name(), nil
}
func extract(archive, dest string, s source, m *manifest) error {
	r, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.Split(f.Name, "/")
		if len(parts) != 2 || parts[0] == "" || parts[0] == "." || parts[0] == ".." || !safeName(parts[1]) {
			return errors.New("组件包路径无效")
		}
		name := parts[1]
		if s.allowed != nil && !s.allowed[name] {
			continue
		}
		if s.allowed == nil {
			ext := strings.ToLower(filepath.Ext(name))
			if ext == ".bat" || ext == ".cmd" || ext == ".vbs" || strings.EqualFold(name, "adb.exe") || strings.EqualFold(name, "AdbWinApi.dll") || strings.EqualFold(name, "AdbWinUsbApi.dll") {
				continue
			}
		}
		for old := range m.Files {
			if strings.EqualFold(old, name) {
				return errors.New("组件包文件冲突")
			}
		}
		if f.UncompressedSize64 > 100<<20 {
			return errors.New("组件文件过大")
		}
		src, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(filepath.Join(dest, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			src.Close()
			return e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(src, (100<<20)+1))
		src.Close()
		ce := out.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
		if n > 100<<20 {
			return errors.New("组件文件过大")
		}
		m.Files[name] = hex.EncodeToString(h.Sum(nil))
	}
	return nil
}

func removeArchive(path string, original error) error {
	if cleanupErr := os.Remove(path); cleanupErr != nil {
		return errors.Join(original, fmt.Errorf("清理组件下载文件失败：%w", cleanupErr))
	}
	return original
}

// Prepare downloads into a new staging directory, verifies archives and writes a file manifest.
func Prepare(parent string) (directory string, err error) {
	stage, e := os.MkdirTemp(parent, ".android-tools-")
	if e != nil {
		return "", e
	}
	ok := false
	defer func() {
		if !ok {
			if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("清理组件暂存目录失败：%w", cleanupErr))
			}
		}
	}()
	m := manifest{Files: map[string]string{}}
	for _, s := range sources {
		archive, e := download(s, stage)
		if e != nil {
			return "", e
		}
		e = extract(archive, stage, s, &m)
		e = removeArchive(archive, e)
		if e != nil {
			return "", e
		}
	}
	for _, n := range required {
		if m.Files[n] == "" {
			return "", fmt.Errorf("组件包缺少 %s", n)
		}
	}
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != trustedManifestSHA {
		return "", errors.New("组件清单校验失败")
	}
	if e = os.WriteFile(filepath.Join(stage, manifestName), b, 0644); e != nil {
		return "", e
	}
	ok = true
	return stage, nil
}

func moveNew(from, to string) error {
	src, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	dst, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFile(src, dst)
}

// Ensure never overwrites a pre-existing tool; missing files are moved atomically.
func Ensure(parent string) (changed bool, err error) {
	target := filepath.Join(parent, Directory)
	if Check(target) == nil {
		return false, nil
	}
	stage, e := Prepare(parent)
	if e != nil {
		return false, e
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("清理组件暂存目录失败：%w", cleanupErr))
		}
	}()
	fresh, e := load(stage)
	if e != nil {
		return false, e
	}
	if _, e = os.Stat(target); os.IsNotExist(e) {
		if e = os.Rename(stage, target); e != nil {
			return false, e
		}
		return true, nil
	} else if e != nil {
		return false, e
	}
	manifestPath := filepath.Join(target, manifestName)
	if _, e = os.Stat(manifestPath); e == nil {
		if _, e = load(target); e != nil {
			return false, errors.New("组件清单损坏，请退出应用后移走旧目录")
		}
	} else if !os.IsNotExist(e) {
		return false, e
	}
	for name, want := range fresh.Files {
		got, e := digest(filepath.Join(target, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false, e
		}
		if got != want {
			return false, fmt.Errorf("组件 %s 与发行版不一致，请退出应用后移走旧的 android-tools 目录", name)
		}
	}
	for name := range fresh.Files {
		p := filepath.Join(target, name)
		if _, e = os.Stat(p); os.IsNotExist(e) {
			if e = moveNew(filepath.Join(stage, name), p); e != nil {
				return false, e
			}
		} else if e != nil {
			return false, e
		}
	}
	if _, e = os.Stat(manifestPath); os.IsNotExist(e) {
		if e = moveNew(filepath.Join(stage, manifestName), manifestPath); e != nil {
			return false, e
		}
	} else if e != nil {
		return false, e
	} else {
		existing, e := load(target)
		if e != nil {
			return false, errors.New("组件清单损坏，请退出应用后移走旧目录")
		}
		for n, h := range fresh.Files {
			if existing.Files[n] != h {
				return false, errors.New("组件清单与发行版不一致，请退出应用后移走旧目录")
			}
		}
	}
	if e = Check(target); e != nil {
		return false, e
	}
	return true, nil
}
