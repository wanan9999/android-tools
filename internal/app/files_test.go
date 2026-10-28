package app

import (
	"errors"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestListRemoteParsesNamesAndQuotesDirectory(t *testing.T) {
	var args []string
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, a ...string) (string, error) {
		args = append([]string(nil), a...)
		return "fhello world\x00d资料\x00f单'引号\x00z\x00", nil
	}}
	got, err := e.listRemote("serial", "/sdcard/a'b")
	if err != nil {
		t.Fatal(err)
	}
	want := []remoteFile{{"资料", true}, {"hello world", false}, {"单'引号", false}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if len(args) != 6 || args[0] != "-s" || args[1] != "serial" || args[2] != "exec-out" || !strings.Contains(args[5], "a") {
		t.Fatalf("command: %#v", args)
	}
	if shellQuote("a'b") != "'a'\\''b'" {
		t.Fatal("shell quoting incorrect")
	}
	if _, err = e.listRemote("serial", "relative"); err == nil {
		t.Fatal("relative path accepted")
	}
}
func TestListRemoteRejectsBadRecords(t *testing.T) {
	e := &engine{adb: "adb", execute: func(string, time.Duration, string, ...string) (string, error) { return "f../bad\x00", nil }}
	if _, err := e.listRemote("serial", "/sdcard"); err == nil {
		t.Fatal("unsafe name accepted")
	}
}
func TestPullFileNeverOverwritesLocalFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "a.txt")
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		if args[2] != "pull" {
			return "", errors.New("unexpected")
		}
		return "", os.WriteFile(args[4], []byte("new"), 0600)
	}}
	if err := e.pullFile("serial", "/sdcard/a.txt", target); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "new" {
		t.Fatalf("%s %v", b, err)
	}
	if err := e.pullFile("serial", "/sdcard/a.txt", target); err == nil {
		t.Fatal("overwrote existing file")
	}
}
func TestFilesViewButtons(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {960, 680}} {
		a := &app{engine: newEngine(), page: "files", files: fileState{serial: "phone", directory: "/sdcard", entries: []remoteFile{{"folder", true}, {"file.txt", false}}}}
		tt := ui.NewTester(a.view, size[0], size[1])
		for _, label := range []string{"文件管理", "上传文件", "刷新文件", "folder", "file.txt", "下载 file.txt", "重命名 file.txt", "删除 file.txt", "重命名 folder", "删除 folder"} {
			if _, ok := tt.Find(label); !ok {
				t.Errorf("%v: missing %s", size, label)
			}
		}
	}
}

func TestFilesPreview(t *testing.T) {
	output := os.Getenv("ANDROID_TOOLS_FILES_PREVIEW")
	if output == "" {
		return
	}
	a := &app{engine: newEngine(), page: "files", files: fileState{serial: "phone", directory: "/sdcard", entries: []remoteFile{{"DCIM", true}, {"Download", true}, {"照片 01.jpg", false}, {"example.txt", false}}}}
	tt := ui.NewTester(a.view, 960, 680)
	f, err := os.Create(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestPushFileChecksAndStages(t *testing.T) {
	calls := 0
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		calls++
		switch calls {
		case 1:
			if args[1] != "serial" || !strings.Contains(args[len(args)-1], "exists") {
				t.Fatal(args)
			}
			return "missing", nil
		case 2:
			if args[2] != "push" || !strings.Contains(args[4], ".android-tools-") {
				t.Fatal(args)
			}
			return "", nil
		case 3:
			if !strings.Contains(args[len(args)-1], "mv -n -T") {
				t.Fatal(args)
			}
			return "moved", nil
		case 4:
			if !strings.Contains(args[len(args)-1], "rm -f") {
				t.Fatal(args)
			}
			return "", nil
		default:
			return "", errors.New("unexpected")
		}
	}}
	if err := e.pushFile("serial", "local", "/sdcard", "name.txt"); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("calls %d", calls)
	}
}
func TestPushFileStopsOnConflict(t *testing.T) {
	calls := 0
	e := &engine{adb: "adb", execute: func(string, time.Duration, string, ...string) (string, error) { calls++; return "exists", nil }}
	if err := e.pushFile("serial", "local", "/sdcard", "name.txt"); err == nil || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func TestRemoteChangesValidateNamesAndResults(t *testing.T) {
	calls := 0
	response := "renamed"
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		calls++
		if args[1] != "serial" || args[2] != "exec-out" {
			t.Fatal(args)
		}
		return response, nil
	}}
	if err := e.renameRemote("serial", "/sdcard", "x'$(touch bad)", "新名字"); err != nil {
		t.Fatal(err)
	}
	response = "exists"
	if err := e.renameRemote("serial", "/sdcard", "old", "new"); err == nil {
		t.Fatal("rename conflict accepted")
	}
	response = "deleted"
	if err := e.deleteRemote("serial", "/sdcard", "folder"); err != nil {
		t.Fatal(err)
	}
	before := calls
	for _, name := range []string{"..", ".", "a/b", ""} {
		if e.deleteRemote("serial", "/sdcard", name) == nil {
			t.Fatalf("unsafe name %q accepted", name)
		}
	}
	if calls != before {
		t.Fatal("invalid names invoked adb")
	}
}
func TestRenameModalRejectsInvalidName(t *testing.T) {
	a := &app{engine: newEngine(), page: "files", files: fileState{serial: "phone", directory: "/sdcard", entries: []remoteFile{{"file.txt", false}}}}
	tt := ui.NewTester(a.view, 960, 680)
	if err := tt.Click("重命名 file.txt"); err != nil {
		t.Fatal(err)
	}
	a.files.renameValue = "../bad"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if !a.files.renameOpen || a.files.renameError == "" {
		t.Fatal("invalid name accepted")
	}
}

func TestListRemoteRejectsIncompleteAndInaccessibleOutput(t *testing.T) {
	for _, output := range []string{"", "ffile\x00", "e\x00", "x\x00z\x00", "sh: error\n"} {
		e := &engine{adb: "adb", execute: func(string, time.Duration, string, ...string) (string, error) { return output, nil }}
		if _, err := e.listRemote("serial", "/sdcard"); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
}
func TestRealADBFileList(t *testing.T) {
	adb, serial := os.Getenv("ANDROID_TOOLS_TEST_ADB"), os.Getenv("ANDROID_TOOLS_TEST_ADB_SERIAL")
	if adb == "" || serial == "" {
		t.Skip("set official adb and serial for hardware test")
	}
	e := &engine{adb: adb}
	entries, err := e.listRemote(serial, "/sdcard")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("listed %d entries", len(entries))
	if _, err = e.listRemote(serial, "/sdcard/.android-tools-nonexistent-test-dir"); err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestFileChangesAgainstShell(t *testing.T) {
	shell := os.Getenv("ANDROID_TOOLS_TEST_SHELL")
	if shell == "" {
		t.Skip("set ANDROID_TOOLS_TEST_SHELL to a POSIX shell")
	}
	dir := t.TempDir()
	slash := filepath.ToSlash(dir)
	if len(slash) < 3 || slash[1] != ':' {
		t.Fatal("unexpected test path")
	}
	remote := "/" + strings.ToLower(slash[:1]) + slash[2:]
	source := "a'$(echo injected).txt"
	destination := "已重命名.txt"
	if err := os.WriteFile(filepath.Join(dir, source), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &engine{adb: "adb", execute: func(_ string, _ time.Duration, _ string, args ...string) (string, error) {
		if len(args) != 6 || args[2] != "exec-out" || args[3] != "sh" || args[4] != "-c" {
			return "", errors.New("unexpected command")
		}
		out, err := exec.Command(shell, "-c", args[5]).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}}
	entries, err := e.listRemote("serial", remote)
	if err != nil || len(entries) != 1 || entries[0].name != source {
		t.Fatalf("list: %+v %v", entries, err)
	}
	if err = e.renameRemote("serial", remote, source, destination); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, source), []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.renameRemote("serial", remote, source, destination); err == nil {
		t.Fatal("conflict accepted")
	}
	if data, err := os.ReadFile(filepath.Join(dir, destination)); err != nil || string(data) != "original" {
		t.Fatalf("destination modified: %s %v", data, err)
	}
	if err = e.deleteRemote("serial", remote, source); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, source)); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if err = e.deleteRemote("serial", remote, destination); err != nil {
		t.Fatal(err)
	}
}

func TestRealADBFileOperations(t *testing.T) {
	adb, serial, socket := os.Getenv("ANDROID_TOOLS_TEST_ADB"), os.Getenv("ANDROID_TOOLS_TEST_ADB_SERIAL"), os.Getenv("ANDROID_TOOLS_TEST_ADB_SOCKET")
	if adb == "" || serial == "" || socket == "" {
		t.Skip("set adb, serial and isolated socket for hardware test")
	}
	e := &engine{adb: adb, socket: socket}
	nonce, err := randomHex(12)
	if err != nil {
		t.Fatal(err)
	}
	dir := "/sdcard/.android-tools-test-" + nonce
	created, err := e.run(adb, 10*time.Second, "", "-s", serial, "exec-out", "sh", "-c", "mkdir -- "+shellQuote(dir)+" && printf created")
	if err != nil || created != "created" {
		t.Fatalf("create test directory: %q %v", created, err)
	}
	original, renamed := "测试 '文件'.txt", "重命名 文件.txt"
	defer func() {
		cleanup := "rm -f -- " + shellQuote(dir+"/"+original) + " " + shellQuote(dir+"/"+renamed) + "; rmdir -- " + shellQuote(dir) + " && printf clean"
		out, err := e.run(adb, 10*time.Second, "", "-s", serial, "exec-out", "sh", "-c", cleanup)
		if err != nil || out != "clean" {
			t.Errorf("test directory cleanup failed: %q %v", out, err)
		}
	}()
	local := filepath.Join(t.TempDir(), "source.txt")
	content := []byte("android-tools production verification\n")
	if err = os.WriteFile(local, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.pushFile(serial, local, dir, original); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if err = e.pushFile(serial, local, dir, original); err == nil {
		t.Fatal("upload overwrote existing target")
	}
	entries, err := e.listRemote(serial, dir)
	if err != nil || len(entries) != 1 || entries[0].name != original {
		t.Fatalf("list: %+v %v", entries, err)
	}
	if err = e.renameRemote(serial, dir, original, renamed); err != nil {
		t.Fatalf("rename: %v", err)
	}
	saved := filepath.Join(t.TempDir(), "saved.txt")
	if err = e.pullFile(serial, dir+"/"+renamed, saved); err != nil {
		t.Fatalf("download: %v", err)
	}
	data, err := os.ReadFile(saved)
	if err != nil || !reflect.DeepEqual(data, content) {
		t.Fatalf("downloaded contents differ: %v", err)
	}
	if err = e.deleteRemote(serial, dir, renamed); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
