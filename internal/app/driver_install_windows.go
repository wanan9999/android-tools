package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"android-tools/internal/tooldeps"
	"golang.org/x/sys/windows"
)

const googleDriverURL = "https://catalog.s.download.windowsupdate.com/d/msdownload/update/driver/drvs/2016/11/20957257_58efff824f4c9455498f94b08e96b26d0a632963.cab"
const googleDriverSHA256 = "4e89f4519cd3c82ceda4676ccf1fb1a2be2d4213e756309ca2ff43b2fcc32e73"

func verifyDriverCatalog(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	file := windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	data := windows.WinTrustData{Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE, RevocationChecks: windows.WTD_REVOKE_NONE, UnionChoice: windows.WTD_CHOICE_FILE, StateAction: windows.WTD_STATEACTION_VERIFY, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&file)}
	err = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	closeErr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	if err != nil {
		return fmt.Errorf("驱动签名校验失败：%w", err)
	}
	return closeErr
}

func downloadGoogleDriver(ctx context.Context, dir string, options tooldeps.DownloadOptions) (string, error) {
	client := &http.Client{Timeout: 2 * time.Minute}
	if options.Client != nil {
		copyClient := *options.Client
		client = &copyClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), "catalog.s.download.windowsupdate.com") {
			return errors.New("驱动下载地址无效")
		}
		return nil
	}
	options.Context, options.Client = ctx, client
	path := filepath.Join(dir, "driver.cab")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	digest, err := tooldeps.Download(options, "Fastboot 驱动", googleDriverURL, f, 32<<20)
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if digest != googleDriverSHA256 {
		return "", errors.New("驱动包校验失败")
	}
	return path, nil
}

func prepareGoogleDriver(ctx context.Context, dir string, options tooldeps.DownloadOptions) (string, error) {
	cab, err := downloadGoogleDriver(ctx, dir, options)
	if err != nil {
		return "", err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(system, "expand.exe"), "-F:*", cab, dir)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true}
	if err = cmd.Run(); err != nil {
		return "", fmt.Errorf("无法解压驱动：%w", err)
	}
	if err = verifyDriverCatalog(filepath.Join(dir, "androidwinusb.cat")); err != nil {
		return "", err
	}
	inf := filepath.Join(dir, "android_winusb.inf")
	if _, err = os.Stat(inf); err != nil {
		return "", errors.New("驱动文件不完整")
	}
	return inf, nil
}

type shellExecuteInfo struct {
	size, mask              uint32
	hwnd                    windows.Handle
	verb, file, params, dir *uint16
	show                    int32
	instance                windows.Handle
	idList                  uintptr
	class                   *uint16
	key                     windows.Handle
	hotkey                  uint32
	icon                    windows.Handle
	process                 windows.Handle
}

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func installGoogleDriver(inf string) error {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(filepath.Join(system, "pnputil.exe"))
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString("/add-driver " + windows.EscapeArg(inf) + " /install")
	if err != nil {
		return err
	}
	info := shellExecuteInfo{size: uint32(unsafe.Sizeof(shellExecuteInfo{})), mask: 0x40, verb: verb, file: file, params: args, show: windows.SW_HIDE}
	ok, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return callErr
	}
	if info.process == 0 {
		return errors.New("无法确认驱动安装结果")
	}
	defer func() { _ = windows.CloseHandle(info.process) }()
	if _, err = windows.WaitForSingleObject(info.process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err = windows.GetExitCodeProcess(info.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("驱动安装失败（代码 %d）", code)
	}
	return nil
}

func eligibleFastbootCandidate(instance string) error {
	candidates, err := scanFastbootCandidates()
	if err != nil {
		return errors.New("无法检查目标设备，请重试")
	}
	for _, c := range candidates {
		if strings.EqualFold(c.Instance, instance) {
			return nil
		}
	}
	return errors.New("目标设备已断开，请重新连接")
}

// SetupAPI compares the actual device hardware and compatible IDs with the
// signed INF, taking Windows architecture and manufacturer sections into account.
func matchDriverInfo(set windows.DevInfo, info *windows.DevInfoData, inf string) (bool, error) {
	path, err := windows.UTF16FromString(inf)
	if err != nil {
		return false, err
	}
	if len(path) > windows.MAX_PATH {
		return false, errors.New("驱动路径过长")
	}
	params, err := set.DeviceInstallParams(info)
	if err != nil {
		return false, err
	}
	params.Flags |= windows.DI_ENUMSINGLEINF
	params.FlagsEx |= windows.DI_FLAGSEX_ALLOWEXCLUDEDDRVS
	if err = params.SetDriverPath(inf); err != nil {
		return false, err
	}
	if err = set.SetDeviceInstallParams(info, params); err != nil {
		return false, err
	}
	if err = set.BuildDriverInfoList(info, windows.SPDIT_COMPATDRIVER); err != nil {
		return false, err
	}
	defer func() { _ = set.DestroyDriverInfoList(info, windows.SPDIT_COMPATDRIVER) }()
	_, err = set.EnumDriverInfo(info, windows.SPDIT_COMPATDRIVER, 0)
	if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
		return false, nil
	}
	return err == nil, err
}

func matchDriverForDevice(instance, inf string) (bool, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "USB", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return false, err
	}
	defer func() { _ = set.Close() }()
	for i := 0; ; i++ {
		info, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return false, errors.New("目标设备已断开，请重新连接")
		}
		if err != nil {
			return false, err
		}
		id, err := windows.SetupDiGetDeviceInstanceId(set, info)
		if err == nil && strings.EqualFold(id, instance) {
			return matchDriverInfo(set, info, inf)
		}
	}
}

func fastbootHasUniqueSerial(devices []device, serial string) bool {
	if serial == "" {
		return false
	}
	count := 0
	for _, d := range devices {
		if strings.EqualFold(d.Serial, serial) {
			count++
		}
	}
	return count == 1
}

func (e *engine) installFastbootDriver(parent context.Context, instance, serial string, options tooldeps.DownloadOptions, progress func(string), confirm func() bool) error {
	if err := eligibleFastbootCandidate(instance); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "android-tools-driver-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	inf, err := prepareGoogleDriver(ctx, dir, options)
	if err != nil {
		return err
	}
	if err = eligibleFastbootCandidate(instance); err != nil {
		return err
	}
	if progress != nil {
		progress("正在匹配设备和签名驱动…")
	}
	matched, err := matchDriverForDevice(instance, inf)
	if err != nil {
		return fmt.Errorf("无法匹配驱动，请使用厂商驱动：%w", err)
	}
	if !matched {
		return errors.New("未找到与此设备匹配的驱动，请使用手机厂商提供的驱动")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if confirm == nil || !confirm() {
		return errors.New("已取消驱动安装")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = eligibleFastbootCandidate(instance); err != nil {
		return err
	}
	matched, err = matchDriverForDevice(instance, inf)
	if err != nil {
		return fmt.Errorf("无法重新确认驱动匹配：%w", err)
	}
	if !matched {
		return errors.New("设备已变化，驱动不再匹配")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if progress != nil {
		progress("等待管理员确认并安装驱动…")
	}
	if err = installGoogleDriver(inf); err != nil {
		return err
	}
	for i := 0; i < 6; i++ {
		devices, e := e.listFastboot()
		if e == nil && fastbootHasUniqueSerial(devices, serial) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("驱动安装已结束，但设备尚未被 Fastboot 识别；请检查连接或打开设备管理器")
}
