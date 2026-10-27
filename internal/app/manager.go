package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type identity struct {
	Hardware, GUID, Model                                             string
	Brand, Manufacturer, Device, Android, SecurityPatch, BuildID, ABI string
	SDK                                                               int
}
type savedDevice struct {
	Hardware string `json:"hardware"`
	GUID     string `json:"guid,omitempty"`
	Model    string `json:"model,omitempty"`
	Address  string `json:"address,omitempty"`
	Attempts int    `json:"attempts,omitempty"`
	RetryAt  int64  `json:"retry_at,omitempty"`
}
type registry struct {
	mu      sync.Mutex
	path    string
	Devices []savedDevice `json:"devices"`
}

func openRegistry(path string) (*registry, error) {
	r := &registry{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("设备记录无法读取：%w", err)
	}
	seen := map[string]bool{}
	guids := map[string]bool{}
	for _, d := range r.Devices {
		if d.Hardware == "" || seen[d.Hardware] || (d.GUID != "" && guids[d.GUID]) {
			return nil, errors.New("设备记录存在重复或缺少身份")
		}
		seen[d.Hardware] = true
		if d.GUID != "" {
			guids[d.GUID] = true
		}
	}
	return r, nil
}
func (r *registry) snapshot() []savedDevice {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]savedDevice(nil), r.Devices...)
}
func (r *registry) save(d savedDevice) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := append([]savedDevice(nil), r.Devices...)
	found := false
	for _, old := range next {
		if old.Hardware != d.Hardware && d.GUID != "" && old.GUID == d.GUID {
			return errors.New("无线身份已绑定另一台手机")
		}
	}
	for i, old := range next {
		if old.Hardware == d.Hardware {
			if d.GUID == "" {
				d.GUID = old.GUID
			}
			if d.Address == "" {
				d.Address = old.Address
			}
			if d == old {
				return nil
			}
			next[i] = d
			found = true
			break
		}
	}
	if !found {
		next = append(next, d)
	}
	if err := r.write(next); err != nil {
		return err
	}
	r.Devices = next
	return nil
}
func (r *registry) remove(hardware string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := make([]savedDevice, 0, len(r.Devices))
	for _, d := range r.Devices {
		if d.Hardware != hardware {
			next = append(next, d)
		}
	}
	if err := r.write(next); err != nil {
		return err
	}
	r.Devices = next
	return nil
}
func (r *registry) write(devices []savedDevice) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(struct {
		Devices []savedDevice `json:"devices"`
	}{devices}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(r.path), ".devices-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(name, r.path)
}

var propertyLine = regexp.MustCompile(`(?m)^\[([^]]+)\]: \[([^]]*)\]\r?$`)
var guidSuffix = regexp.MustCompile(` \([2-9][0-9]*\)$| \(1[0-9]+\)$`)

func serviceGUID(s string) string {
	s = strings.TrimSuffix(strings.TrimSuffix(s, "."), "._adb-tls-connect._tcp")
	return guidSuffix.ReplaceAllString(s, "")
}
func parseIdentity(serial, out string) (identity, error) {
	p := map[string]string{}
	for _, m := range propertyLine.FindAllStringSubmatch(out, -1) {
		p[m[1]] = m[2]
	}
	h := p["ro.serialno"]
	if h == "" {
		h = p["ro.boot.serialno"]
	}
	if h == "" || len(h) > 160 || strings.EqualFold(h, "unknown") || strings.EqualFold(h, "null") || strings.EqualFold(h, "0123456789abcdef") || strings.Trim(h, "0") == "" {
		return identity{}, errors.New("无法读取手机稳定身份，请检查设备授权")
	}
	guid := p["persist.adb.wifi.guid"]
	if strings.Contains(serial, "._adb-tls-connect._tcp") {
		transport := serviceGUID(serial)
		if guid != "" && guid != transport {
			return identity{}, errors.New("设备无线身份不一致")
		}
		guid = transport
	}
	sdk, err := strconv.Atoi(p["ro.build.version.sdk"])
	if err != nil || sdk <= 0 {
		sdk = 0
	}
	return identity{Hardware: h, GUID: guid, Model: p["ro.product.model"], Brand: p["ro.product.brand"], Manufacturer: p["ro.product.manufacturer"], Device: p["ro.product.device"], Android: p["ro.build.version.release"], SecurityPatch: p["ro.build.version.security_patch"], BuildID: p["ro.build.display.id"], ABI: p["ro.product.cpu.abilist"], SDK: sdk}, nil
}
func (e *engine) identify(serial string) (identity, error) {
	out, err := e.run(e.adb, 5*time.Second, "", "-s", serial, "shell", "getprop")
	if err != nil {
		return identity{}, err
	}
	return parseIdentity(serial, out)
}
func (e *engine) checkedIdentity(serial, guid, hardware string) (identity, error) {
	id, err := e.identify(serial)
	if err != nil {
		return id, err
	}
	if id.GUID == "" && guid != "" {
		ds, err := e.adbDevices()
		if err != nil {
			return id, err
		}
		var aliases []string
		for _, d := range ds {
			if d.Kind == "ADB" && d.State == "device" && strings.Contains(d.Serial, "._adb-tls-connect._tcp") && serviceGUID(d.Serial) == guid {
				aliases = append(aliases, d.Serial)
			}
		}
		if len(aliases) == 1 {
			other, err := e.identify(aliases[0])
			if err != nil {
				return id, err
			}
			if other.Hardware != id.Hardware {
				return id, errors.New("无线别名属于另一台手机")
			}
			id = other
		} else if hardware != "" && id.Hardware == hardware {
			id.GUID = guid
		} else {
			return id, errors.New("无法核验无线身份，请检查无线调试和网络")
		}
	}
	if guid != "" && id.GUID != guid {
		return id, errors.New("连接设备与配对身份不一致")
	}
	if hardware != "" && id.Hardware != hardware {
		return id, errors.New("连接地址属于另一台手机")
	}
	return id, nil
}
func (e *engine) connectVerified(addr, guid, hardware string) (identity, error) {
	devices, _ := e.adbDevices()
	existing := false
	for _, d := range devices {
		if d.Serial == addr && d.State == "device" {
			existing = true
			break
		}
	}
	if err := e.connect(addr); err != nil {
		return identity{}, err
	}
	id, err := e.checkedIdentity(addr, guid, hardware)
	if err != nil {
		if !existing {
			_, _ = e.run(e.adb, 4*time.Second, "", "disconnect", addr)
		}
		return identity{}, err
	}
	return id, nil
}

type manager struct {
	engine       *engine
	registry     *registry
	mu           sync.Mutex
	refreshMu    sync.Mutex
	pair         *pairSession
	generation   uint64
	pairInterval time.Duration
}

func newManager(e *engine, r *registry) *manager {
	return &manager{engine: e, registry: r}
}
func (m *manager) remember(id identity, address string) error {
	return m.registry.save(savedDevice{Hardware: id.Hardware, GUID: id.GUID, Model: id.Model, Address: address})
}
func wirelessSDKError(sdk int) error {
	if sdk <= 0 {
		return errors.New("无法读取手机 Android SDK 版本，请检查设备授权")
	}
	if sdk < 30 {
		return errors.New("无线配对需要 Android 11 或以上")
	}
	return nil
}
func (m *manager) connect(addr, guid string) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	var expected string
	known := m.registry.snapshot()
	for _, d := range known {
		if guid != "" && d.GUID == guid {
			expected = d.Hardware
			break
		}
	}
	for _, d := range known {
		if d.Address == addr {
			if expected != "" && expected != d.Hardware {
				return errors.New("连接地址属于另一台已登记手机")
			}
			expected = d.Hardware
			if guid == "" {
				guid = d.GUID
			}
		}
	}
	id, err := m.engine.connectVerified(addr, guid, expected)
	if err != nil {
		return err
	}
	if guid != "" {
		if err := wirelessSDKError(id.SDK); err != nil {
			return err
		}
	}
	return m.remember(id, addr)
}
func (m *manager) connectKnown(addr, hardware string) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	var record *savedDevice
	for _, d := range m.registry.snapshot() {
		if d.Hardware == hardware {
			d := d
			record = &d
			break
		}
	}
	if record == nil {
		return errors.New("原设备记录不存在，请重新选择")
	}
	if record.GUID == "" {
		return errors.New("该设备没有无线身份，请先配对")
	}
	id, err := m.engine.connectVerified(addr, record.GUID, record.Hardware)
	if err != nil {
		return err
	}
	if err := wirelessSDKError(id.SDK); err != nil {
		return err
	}
	return m.remember(id, addr)
}
func validConnectionAddress(pairAddress, address string) error {
	if err := validAddress(address); err != nil {
		return err
	}
	if pairAddress == "" {
		return nil
	}
	pairHost, _, err := net.SplitHostPort(pairAddress)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if pairAddress == address {
		return errors.New("请使用无线调试首页的连接端口，不是配对端口")
	}
	if !net.ParseIP(strings.Split(pairHost, "%")[0]).Equal(net.ParseIP(strings.Split(host, "%")[0])) {
		return errors.New("连接地址应属于刚配对的手机")
	}
	return nil
}
func (m *manager) refresh() ([]device, []service, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	ds, err := m.engine.adbDevices()
	var services []service
	if err == nil {
		services = m.engine.services()
	}
	saved := m.registry.snapshot()
	online := map[string]bool{}
	var result []device
	for _, d := range ds {
		if d.Kind != "ADB" || d.State != "device" {
			result = append(result, d)
			continue
		}
		id, e := m.engine.identify(d.Serial)
		if e != nil {
			d.State = "identity_error"
			result = append(result, d)
			continue
		}
		mismatch := false
		for _, old := range saved {
			if old.Address != "" && old.Address == d.Serial && old.Hardware != id.Hardware {
				mismatch = true
				break
			}
		}
		if mismatch {
			d.State = "identity_error"
			result = append(result, d)
			continue
		}
		if online[id.Hardware] {
			continue
		}
		online[id.Hardware] = true
		d.Hardware = id.Hardware
		if id.Model != "" {
			d.Model = id.Model
		}
		wireless := strings.Contains(d.Serial, ":") || strings.Contains(d.Serial, "._adb-tls-connect._tcp")
		if !wireless || id.GUID != "" {
			address := ""
			if wireless && validAddress(d.Serial) == nil {
				address = d.Serial
			} else if wireless {
				for _, service := range services {
					if strings.Contains(service.Kind, "_adb-tls-connect") && serviceGUID(service.Name) == id.GUID {
						address = service.Address
						break
					}
				}
			}
			if e := m.remember(id, address); e != nil {
				d.State = "identity_error"
				delete(online, id.Hardware)
			}
		}
		result = append(result, d)
	}
	attempts := 0
	for _, old := range saved {
		if online[old.Hardware] {
			continue
		}
		d := device{Serial: old.Address, Hardware: old.Hardware, Model: old.Model, Kind: "ADB", State: "offline"}
		result = append(result, d)
		if err != nil || old.GUID == "" || time.Now().Unix() < old.RetryAt || attempts >= 3 {
			continue
		}
		attempts++
		addr := old.Address
		for _, s := range services {
			if strings.Contains(s.Kind, "_adb-tls-connect") && serviceGUID(s.Name) == old.GUID {
				addr = s.Address
				break
			}
		}
		if addr != "" {
			if id, e := m.engine.connectVerified(addr, old.GUID, old.Hardware); e == nil {
				if e = m.remember(id, addr); e != nil {
					return result, services, e
				}
				continue
			}
		}
		old.Attempts++
		if old.Attempts > 5 {
			old.Attempts = 5
		}
		delay := 5 * (1 << old.Attempts)
		if delay > 120 {
			delay = 120
		}
		old.RetryAt = time.Now().Add(time.Duration(delay) * time.Second).Unix()
		if e := m.registry.save(old); e != nil {
			return result, services, e
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].State == "device" && result[j].State != "device" })
	return result, services, err
}
func (m *manager) forget(hardware string) error {
	m.cancelPair()
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	return m.registry.remove(hardware)
}
