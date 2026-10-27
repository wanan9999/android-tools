package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	winregistry "golang.org/x/sys/windows/registry"
)

type fastbootCandidate struct {
	Instance, Serial, Name, Driver string
	Problem                        uint32
	InterfaceMismatch              bool
}
type fastbootSnapshot struct {
	devices                []device
	candidates             []fastbootCandidate
	toolError, deviceError bool
}

// Only the Android fastboot USB protocol is recognized, not generic Android devices.
func fastbootCompatible(ids []string) bool {
	for _, id := range ids {
		id = strings.ToUpper(id)
		if strings.HasPrefix(id, "USB\\") && strings.Contains(id, "CLASS_FF&SUBCLASS_42&PROT_03") {
			return true
		}
	}
	return false
}
func scanFastbootCandidates() ([]fastbootCandidate, error) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "USB", 0, windows.DIGCF_PRESENT|windows.DIGCF_ALLCLASSES, 0, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = set.Close() }()
	var out []fastbootCandidate
	for i := 0; ; i++ {
		info, e := set.EnumDeviceInfo(i)
		if errors.Is(e, windows.ERROR_NO_MORE_ITEMS) {
			break
		}
		if e != nil {
			return nil, e
		}
		raw, e := set.DeviceRegistryProperty(info, windows.SPDRP_COMPATIBLEIDS)
		if e != nil {
			continue
		}
		ids, ok := raw.([]string)
		if !ok || !fastbootCompatible(ids) {
			continue
		}
		instance, e := windows.SetupDiGetDeviceInstanceId(set, info)
		if e != nil || instance == "" {
			continue
		}
		name, _ := set.DeviceRegistryProperty(info, windows.SPDRP_DEVICEDESC)
		label, _ := name.(string)
		parts := strings.Split(instance, `\`)
		serial := ""
		if len(parts) >= 3 {
			serial = parts[len(parts)-1]
		}
		candidate := fastbootCandidate{Instance: instance, Serial: serial, Name: label}
		if value, e := set.DeviceRegistryProperty(info, windows.SPDRP_SERVICE); e == nil {
			candidate.Driver, _ = value.(string)
		}
		var status, problem uint32
		if windows.CM_Get_DevNode_Status(&status, &problem, info.DevInst, 0) == nil {
			candidate.Problem = problem
		}
		if strings.EqualFold(candidate.Driver, "WinUSB") {
			if handle, e := set.OpenDevRegKey(info, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DEV, windows.KEY_READ); e == nil {
				key := winregistry.Key(handle)
				if guids, _, e := key.GetStringsValue("DeviceInterfaceGUIDs"); e == nil && len(guids) > 0 {
					candidate.InterfaceMismatch = true
					for _, guid := range guids {
						if strings.EqualFold(guid, "{F72FE0D4-CBCB-407D-8814-9ED673D0DD6B}") {
							candidate.InterfaceMismatch = false
						}
					}
				}
				_ = key.Close()
			}
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}
func unmatchedFastboot(candidates []fastbootCandidate, devices []device) []fastbootCandidate {
	candidateCount := map[string]int{}
	deviceCount := map[string]int{}
	for _, c := range candidates {
		if c.Serial != "" {
			candidateCount[strings.ToUpper(c.Serial)]++
		}
	}
	for _, d := range devices {
		if d.Serial != "" {
			deviceCount[strings.ToUpper(d.Serial)]++
		}
	}
	var out []fastbootCandidate
	for _, c := range candidates {
		key := strings.ToUpper(c.Serial)
		if key != "" && candidateCount[key] == 1 && deviceCount[key] == 1 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// CM_Register_Notification supports all interface classes, including vendor WinUSB GUIDs.
type cmNotifyFilter struct {
	Size, Flags, Type, Reserved uint32
	Data                        [200]uint16
}

var cmLibrary = windows.NewLazySystemDLL("cfgmgr32.dll")
var cmRegister = cmLibrary.NewProc("CM_Register_Notification")
var cmUnregister = cmLibrary.NewProc("CM_Unregister_Notification")

func subscribeUSB(ch chan<- struct{}) (func(), error) {
	filter := cmNotifyFilter{Size: uint32(unsafe.Sizeof(cmNotifyFilter{})), Flags: 1}
	callback := windows.NewCallback(func(_, _, _, _, _ uintptr) uintptr {
		select {
		case ch <- struct{}{}:
		default:
		}
		return 0
	})
	var handle uintptr
	result, _, _ := cmRegister.Call(uintptr(unsafe.Pointer(&filter)), 0, callback, uintptr(unsafe.Pointer(&handle)))
	if result != 0 {
		return nil, fmt.Errorf("注册设备通知失败：%d", result)
	}
	return func() { cmUnregister.Call(handle) }, nil
}
func (e *engine) watchFastboot(ctx context.Context, manual <-chan struct{}, changed func(fastbootSnapshot)) {
	events := make(chan struct{}, 1)
	closeNotify, err := subscribeUSB(events)
	interval := 30 * time.Second
	if err != nil {
		interval = 5 * time.Second
	} else {
		defer closeNotify()
	}
	var last fastbootSnapshot
	haveLast := false
	scan := func() {
		devices, toolErr := e.listFastboot()
		candidates, deviceErr := scanFastbootCandidates()
		if toolErr == nil {
			candidates = unmatchedFastboot(candidates, devices)
		}
		snapshot := fastbootSnapshot{devices: devices, candidates: candidates, toolError: toolErr != nil, deviceError: deviceErr != nil}
		if ctx.Err() == nil && (!haveLast || !reflect.DeepEqual(last, snapshot)) {
			last, haveLast = snapshot, true
			changed(snapshot)
		}
	}
	runFastbootWatch(ctx, events, manual, interval, scan)
}
func runFastbootWatch(ctx context.Context, events, manual <-chan struct{}, interval time.Duration, scan func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	scan()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scan()
		case <-manual:
			scan()
		case <-events:
			// Coalesce a burst of interface changes and rescan once more after drivers settle.
			timer := time.NewTimer(350 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			select {
			case <-events:
			default:
			}
			scan()
			timer.Reset(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			select {
			case <-events:
			default:
			}
			scan()
		}
	}
}

func openDeviceManager() error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		return errors.New("无法定位 Windows 系统目录")
	}
	exe, err := windows.UTF16PtrFromString(filepath.Join(root, "System32", "mmc.exe"))
	if err != nil {
		return err
	}
	args, err := windows.UTF16PtrFromString(windows.EscapeArg(filepath.Join(root, "System32", "devmgmt.msc")))
	if err != nil {
		return err
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, exe, args, nil, windows.SW_SHOWNORMAL)
}

func deviceManagerError(err error) string {
	if errors.Is(err, windows.ERROR_CANCELLED) {
		return "已取消权限确认，可从 Windows 开始菜单打开设备管理器。"
	}
	if err != nil {
		return "无法打开设备管理器，请从 Windows 开始菜单打开。"
	}
	return ""
}
