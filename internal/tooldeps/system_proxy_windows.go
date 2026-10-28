package tooldeps

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type userProxyConfig struct {
	AutoDetect             int32
	AutoURL, Proxy, Bypass *uint16
}
type autoProxyOptions struct {
	Flags, Detect uint32
	URL           *uint16
	Reserved      uintptr
	Reserved2     uint32
	AutoLogon     int32
}
type proxyInfo struct {
	Access        uint32
	Proxy, Bypass *uint16
}

var wh = windows.NewLazySystemDLL("winhttp.dll")
var whUser = wh.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var whOpen = wh.NewProc("WinHttpOpen")
var whClose = wh.NewProc("WinHttpCloseHandle")
var whForURL = wh.NewProc("WinHttpGetProxyForUrl")
var whTimeouts = wh.NewProc("WinHttpSetTimeouts")
var globalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

func freeProxy(p *uint16) {
	if p != nil {
		globalFree.Call(uintptr(unsafe.Pointer(p)))
	}
}
func bypassProxy(raw string, u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	hostport := strings.ToLower(u.Host)
	for _, part := range strings.Split(raw, ";") {
		p := strings.ToLower(strings.TrimSpace(part))
		if p == "" {
			continue
		}
		if p == "<local>" && !strings.Contains(host, ".") {
			return true
		}
		if ok, _ := path.Match(p, host); ok {
			return true
		}
		if ok, _ := path.Match(p, hostport); ok {
			return true
		}
	}
	return false
}
func parseSystemProxy(raw string, u *url.URL) (*url.URL, error) {
	var fallback, selected string
	unsupported := false
	for _, part := range strings.Split(raw, ";") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if strings.EqualFold(p, "DIRECT") {
			if fallback == "" {
				return nil, nil
			}
			continue
		}
		if strings.HasPrefix(strings.ToUpper(p), "PROXY ") {
			p = strings.TrimSpace(p[6:])
		} else if strings.HasPrefix(strings.ToUpper(p), "SOCKS5 ") {
			p = "socks5://" + strings.TrimSpace(p[7:])
		} else if strings.HasPrefix(strings.ToUpper(p), "SOCKS ") {
			return nil, errors.New("系统 SOCKS 代理类型不受支持")
		}
		key, v, has := strings.Cut(p, "=")
		if has {
			if strings.EqualFold(key, u.Scheme) {
				selected = v
				break
			}
			if strings.EqualFold(key, "socks") {
				unsupported = true
			}
		} else if fallback == "" {
			fallback = p
		}
	}
	if selected == "" {
		selected = fallback
	}
	if selected == "" {
		if unsupported {
			return nil, errors.New("系统 SOCKS 代理类型不受支持")
		}
		return nil, nil
	}
	if !strings.Contains(selected, "://") {
		selected = "http://" + selected
	}
	p, e := url.Parse(selected)
	if e != nil || p.Host == "" || (p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5") {
		return nil, errors.New("系统代理配置不受支持")
	}
	return p, nil
}
func systemProxy(req *http.Request) (*url.URL, error) {
	cfg := userProxyConfig{}
	r, _, callErr := whUser.Call(uintptr(unsafe.Pointer(&cfg)))
	if r == 0 {
		return nil, fmt.Errorf("无法读取系统代理：%w", callErr)
	}
	defer freeProxy(cfg.AutoURL)
	defer freeProxy(cfg.Proxy)
	defer freeProxy(cfg.Bypass)
	proxyRaw := windows.UTF16PtrToString(cfg.Proxy)
	bypass := windows.UTF16PtrToString(cfg.Bypass)
	hasAutoURL := windows.UTF16PtrToString(cfg.AutoURL) != ""
	if hasAutoURL || cfg.AutoDetect != 0 {
		h, _, e := whOpen.Call(0, 1, 0, 0, 0)
		if h == 0 {
			return nil, fmt.Errorf("系统代理不可用：%w", e)
		}
		defer whClose.Call(h)
		whTimeouts.Call(h, 5000, 5000, 5000, 5000)
		target, e := windows.UTF16PtrFromString(req.URL.String())
		if e != nil {
			return nil, e
		}
		opt := autoProxyOptions{Flags: 1, Detect: 3}
		if hasAutoURL {
			opt.Flags = 2
			opt.Detect = 0
			opt.URL = cfg.AutoURL
		}
		info := proxyInfo{}
		ok, _, e := whForURL.Call(h, uintptr(unsafe.Pointer(target)), uintptr(unsafe.Pointer(&opt)), uintptr(unsafe.Pointer(&info)))
		defer freeProxy(info.Proxy)
		defer freeProxy(info.Bypass)
		if ok != 0 {
			if info.Access == 1 {
				return nil, nil
			}
			if info.Access != 3 || windows.UTF16PtrToString(info.Proxy) == "" {
				return nil, errors.New("系统代理返回了不支持的配置")
			}
			if bypassProxy(windows.UTF16PtrToString(info.Bypass), req.URL) {
				return nil, nil
			}
			return parseSystemProxy(windows.UTF16PtrToString(info.Proxy), req.URL)
		}
		// A missing WPAD configuration permits the Windows manual setting to apply;
		// other failures must not silently bypass an intended proxy.
		if hasAutoURL || !errors.Is(e, windows.Errno(12180)) {
			return nil, fmt.Errorf("无法解析系统代理脚本：%w", e)
		}
	}
	if bypassProxy(bypass, req.URL) {
		return nil, nil
	}
	return parseSystemProxy(proxyRaw, req.URL)
}
