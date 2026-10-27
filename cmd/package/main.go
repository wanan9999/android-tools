// Command package builds an uninstalled Windows ZIP from pinned official tool archives.
package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"android-tools/internal/appicon"
	"android-tools/internal/tooldeps"
)

func main() {
	out := flag.String("out", "android-tools-windows-amd64.zip", "输出 ZIP，不能覆盖已有文件")
	flag.Parse()
	if err := build(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func build(out string) error {
	dest, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if _, err = os.Stat(dest); !os.IsNotExist(err) {
		return errors.New("输出文件已存在或无法检查，未覆盖")
	}
	temp, err := os.MkdirTemp("", "android-tools-release-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	stage := filepath.Join(temp, "source")
	if err = os.MkdirAll(filepath.Join(stage, "internal", "appicon"), 0755); err != nil {
		return err
	}
	for _, n := range []string{"go.mod", "go.sum"} {
		content, e := os.ReadFile(n)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(stage, n), content, 0644); e != nil {
			return e
		}
	}
	appSource := filepath.Join("cmd", "android-tools")
	appStage := filepath.Join(stage, "cmd", "android-tools")
	if err = os.MkdirAll(appStage, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(appSource)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		n := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		content, e := os.ReadFile(filepath.Join(appSource, n))
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(appStage, n), content, 0644); e != nil {
			return e
		}
	}
	appPackage := filepath.Join("internal", "app")
	appPackageStage := filepath.Join(stage, appPackage)
	if err = os.MkdirAll(appPackageStage, 0755); err != nil {
		return err
	}
	entries, err = os.ReadDir(appPackage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		n := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		content, e := os.ReadFile(filepath.Join(appPackage, n))
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(appPackageStage, n), content, 0644); e != nil {
			return e
		}
	}
	for _, n := range []string{"icon.go", "icon.svg", "LUCIDE-LICENSE.txt"} {
		content, e := os.ReadFile(filepath.Join("internal", "appicon", n))
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(stage, "internal", "appicon", n), content, 0644); e != nil {
			return e
		}
	}
	if err = os.MkdirAll(filepath.Join(stage, "internal", "tooldeps"), 0755); err != nil {
		return err
	}
	dep, e := os.ReadFile(filepath.Join("internal", "tooldeps", "tooldeps.go"))
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(stage, "internal", "tooldeps", "tooldeps.go"), dep, 0644); e != nil {
		return e
	}
	iconData, err := appicon.ICO()
	if err != nil {
		return err
	}
	iconFile := filepath.Join(temp, "app.ico")
	if err = os.WriteFile(iconFile, iconData, 0600); err != nil {
		return err
	}
	resource := exec.Command("go", "tool", "rsrc", "-arch", "amd64", "-ico", iconFile, "-o", filepath.Join(appStage, "app_windows_amd64.syso"))
	resource.Dir = stage
	resource.Stdout = os.Stdout
	resource.Stderr = os.Stderr
	if err = resource.Run(); err != nil {
		return err
	}
	binary := filepath.Join(temp, "android-tools.exe")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -H=windowsgui", "-o", binary, "./cmd/android-tools")
	cmd.Dir = stage
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return err
	}
	fmt.Println("下载并校验 Android 组件")
	toolsDir, err := tooldeps.Prepare(temp)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(dest)
		}
	}()
	w := zip.NewWriter(f)
	app, err := w.Create("android-tools.exe")
	if err != nil {
		return err
	}
	src, err := os.Open(binary)
	if err != nil {
		return err
	}
	_, err = io.Copy(app, src)
	src.Close()
	if err != nil {
		return err
	}
	license, err := w.Create("licenses/Lucide.txt")
	if err != nil {
		return err
	}
	if _, err = license.Write(appicon.License); err != nil {
		return err
	}
	files, err := os.ReadDir(toolsDir)
	if err != nil {
		return err
	}
	for _, entry := range files {
		if entry.IsDir() {
			return errors.New("组件目录无效")
		}
		dst, err := w.Create(tooldeps.Directory + "/" + entry.Name())
		if err != nil {
			return err
		}
		src, err := os.Open(filepath.Join(toolsDir, entry.Name()))
		if err != nil {
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err = w.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	fmt.Println("已生成", dest)
	return nil
}
