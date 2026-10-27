package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type pairSession struct {
	ctx           context.Context
	cancel        context.CancelFunc
	address, guid string
	manual        chan string
}

func knownPairDevice(records []savedDevice, guid string) (string, string) {
	for _, d := range records {
		if d.GUID == guid && guid != "" {
			return d.Hardware, d.Address
		}
	}
	return "", ""
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func (m *manager) cancelPair() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	if m.pair != nil {
		m.pair.cancel()
		m.pair = nil
	}
}
func (m *manager) submitQRAddress(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.pair
	if session == nil || session.ctx.Err() != nil || session.guid == "" {
		return errors.New("配对尚未完成或已结束，请重新开始")
	}
	if err := validConnectionAddress(session.address, address); err != nil {
		return err
	}
	select {
	case session.manual <- address:
		return nil
	default:
		return errors.New("正在连接，请稍候")
	}
}
func (m *manager) commitPair(ctx context.Context, gen uint64, serial, address, guid, hardware string, connect bool) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	var id identity
	var err error
	if connect {
		id, err = m.engine.connectVerified(address, guid, hardware)
	} else {
		id, err = m.engine.checkedIdentity(serial, guid, hardware)
	}
	if err != nil {
		return err
	}
	if err := wirelessSDKError(id.SDK); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil || m.generation != gen {
		return context.Canceled
	}
	return m.remember(id, address)
}
func (m *manager) beginQR(notify func(string, string)) (string, error) {
	m.cancelPair()
	serviceID, err := randomHex(8)
	if err != nil {
		return "", err
	}
	secret, err := randomHex(18)
	if err != nil {
		return "", err
	}
	name := "studio-" + serviceID
	payload := "WIFI:T:ADB;S:" + name + ";P:" + secret + ";;"
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	session := &pairSession{ctx: ctx, cancel: cancel, manual: make(chan string, 1)}
	m.mu.Lock()
	m.generation++
	gen := m.generation
	m.pair = session
	m.mu.Unlock()
	go func() {
		defer cancel()
		defer func() {
			m.mu.Lock()
			if m.pair == session {
				m.pair = nil
			}
			m.mu.Unlock()
		}()
		publish := func(state, msg string) {
			m.mu.Lock()
			current := m.pair == session && m.generation == gen
			m.mu.Unlock()
			if current {
				notify(state, msg)
			}
		}
		interval := m.pairInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		timer := time.NewTicker(interval)
		defer timer.Stop()
		var pairAddress, guid, hardware, savedAddress string
		attempts := map[string]time.Time{}
		savedAttempts := 0
		for {
			if guid != "" {
				// ADB may already have connected the phone without a visible mDNS broadcast.
				devices, _ := m.engine.adbDevices()
				for _, d := range devices {
					if d.State != "device" {
						continue
					}
					isGUID := strings.Contains(d.Serial, "._adb-tls-connect._tcp") && serviceGUID(d.Serial) == guid
					isAddress := validAddress(d.Serial) == nil && (d.Serial == savedAddress || validConnectionAddress(pairAddress, d.Serial) == nil)
					if !isGUID && !isAddress {
						continue
					}
					key := "transport:" + d.Serial
					if time.Now().Before(attempts[key]) {
						continue
					}
					attempts[key] = time.Now().Add(20 * time.Second)
					address := ""
					if isAddress {
						address = d.Serial
					}
					if err := m.commitPair(ctx, gen, d.Serial, address, guid, hardware, false); err == nil {
						publish("connected", "连接成功，设备身份已核验")
						return
					} else if errors.Is(err, context.Canceled) {
						return
					}
				}
				for _, s := range m.engine.services() {
					if !strings.Contains(s.Kind, "_adb-tls-connect") || serviceGUID(s.Name) != guid {
						continue
					}
					key := "address:" + s.Address
					if time.Now().Before(attempts[key]) {
						continue
					}
					attempts[key] = time.Now().Add(20 * time.Second)
					if err := m.commitPair(ctx, gen, "", s.Address, guid, hardware, true); err == nil {
						publish("connected", "连接成功，设备身份已核验")
						return
					} else if errors.Is(err, context.Canceled) {
						return
					}
				}
				if savedAddress != "" && savedAttempts < 3 && !time.Now().Before(attempts["saved"]) && !time.Now().Before(attempts["address:"+savedAddress]) {
					savedAttempts++
					attempts["saved"] = time.Now().Add(20 * time.Second)
					if err := m.commitPair(ctx, gen, "", savedAddress, guid, hardware, true); err == nil {
						publish("connected", "连接成功，设备身份已核验")
						return
					} else if errors.Is(err, context.Canceled) {
						return
					}
				}
			}
			select {
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					publish("expired", "添加已超时，已建立的配对信任会保留；可使用已配对设备连接")
				}
				return
			case address := <-session.manual:
				if err := m.commitPair(ctx, gen, "", address, guid, hardware, true); err == nil {
					publish("connected", "连接成功，设备身份已核验")
					return
				} else if errors.Is(err, context.Canceled) {
					return
				} else {
					publish("connecting", fmt.Sprintf("连接未完成：%s。请检查地址或重试", err))
				}
			case <-timer.C:
				if guid != "" {
					continue
				}
				for _, s := range m.engine.services() {
					if !strings.Contains(s.Kind, "_adb-tls-pairing") || s.Name != name {
						continue
					}
					pairAddress = s.Address
					publish("pairing", "正在建立配对")
					guid, err = m.engine.pairSecret(pairAddress, secret)
					secret = ""
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						publish("error", err.Error())
						return
					}
					hardware, savedAddress = knownPairDevice(m.registry.snapshot(), guid)
					m.mu.Lock()
					if m.pair != session || m.generation != gen || ctx.Err() != nil {
						m.mu.Unlock()
						return
					}
					session.address, session.guid = pairAddress, guid
					m.mu.Unlock()
					publish("connecting", "配对成功，正在连接；未自动发现时可填写无线调试首页的连接地址")
					break
				}
			}
		}
	}()
	return payload, nil
}
