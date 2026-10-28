package tooldeps

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, names []string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, n := range names {
		w, e := z.Create("root/" + n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte("fixture:" + n)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func TestEnsureDownloadsRepairsMissingAndRejectsModified(t *testing.T) {
	a := fixture(t, []string{"adb.exe", "fastboot.exe", "AdbWinApi.dll", "AdbWinUsbApi.dll", "NOTICE.txt"})
	b := fixture(t, []string{"scrcpy.exe", "scrcpy-server", "SDL3.dll", "avcodec-63.dll", "avformat-63.dll", "avutil-61.dll", "swresample-7.dll", "libusb-1.0.dll", "LICENSE.txt"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			w.Write(a)
		} else {
			w.Write(b)
		}
	}))
	defer server.Close()
	original := sources
	originalHash := trustedManifestSHA
	defer func() { sources = original; trustedManifestSHA = originalHash }()
	m := manifest{Files: map[string]string{}}
	for _, n := range required {
		m.Files[n] = hash([]byte("fixture:" + n))
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	trustedManifestSHA = hash(encoded)
	sources = []source{{server.URL + "/a", hash(a), map[string]bool{"adb.exe": true, "fastboot.exe": true, "AdbWinApi.dll": true, "AdbWinUsbApi.dll": true, "NOTICE.txt": true}}, {server.URL + "/b", hash(b), nil}}
	parent := t.TempDir()
	changed, e := Ensure(parent, DownloadOptions{})
	if e != nil || !changed {
		t.Fatalf("initial %v %v", changed, e)
	}
	dir := filepath.Join(parent, Directory)
	if e = Check(dir); e != nil {
		t.Fatal(e)
	}
	changed, e = Ensure(parent, DownloadOptions{})
	if e != nil || changed {
		t.Fatalf("existing %v %v", changed, e)
	}
	if e = os.Remove(filepath.Join(dir, "fastboot.exe")); e != nil {
		t.Fatal(e)
	}
	changed, e = Ensure(parent, DownloadOptions{})
	if e != nil || !changed {
		t.Fatalf("repair %v %v", changed, e)
	}
	if e = os.WriteFile(filepath.Join(dir, "adb.exe"), []byte("modified"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = Ensure(parent, DownloadOptions{}); e == nil {
		t.Fatal("modified executable replaced")
	}
	b2, e := os.ReadFile(filepath.Join(dir, "adb.exe"))
	if e != nil || string(b2) != "modified" {
		t.Fatal("existing file changed")
	}
}
func TestRejectsUnsafeArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.zip")
	if e := os.WriteFile(p, fixture(t, []string{"../../bad"}), 0600); e != nil {
		t.Fatal(e)
	}
	m := manifest{Files: map[string]string{}}
	if e := extract(p, t.TempDir(), source{}, &m); e == nil {
		t.Fatal("unsafe path accepted")
	}
}
func TestOfficialDownload(t *testing.T) {
	if os.Getenv("ANDROID_TOOLS_TEST_DOWNLOAD") != "1" {
		t.Skip("需要联网时运行")
	}
	parent := t.TempDir()
	changed, e := Ensure(parent, DownloadOptions{})
	if e != nil || !changed {
		t.Fatalf("download=%v err=%v", changed, e)
	}
	if e = Check(filepath.Join(parent, Directory)); e != nil {
		t.Fatal(e)
	}
}

func TestArchiveCleanupFailurePreservesOriginalError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	original := errors.New("解压失败")
	err := removeArchive(dir, original)
	if !errors.Is(err, original) || !strings.Contains(err.Error(), "清理组件下载文件失败") {
		t.Fatalf("missing errors: %v", err)
	}
	file := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeArchive(file, original); !errors.Is(err, original) {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("archive remains")
	}
}
