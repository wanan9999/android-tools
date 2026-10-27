package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// trackADB reads ADB's device-change stream without starting another daemon.
func (e *engine) trackADB(ctx context.Context, changed func([]device)) {
	for ctx.Err() == nil {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", strings.TrimPrefix(e.socketAddress(), "tcp:"))
		if err == nil {
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			err = readADBEvents(conn, changed)
			stop()
			conn.Close()
			if ctx.Err() == nil {
				changed(nil)
			}
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
func readADBEvents(conn net.Conn, changed func([]device)) error {
	request := "host:track-devices-l"
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(conn, "%04x%s", len(request), request); err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Time{})
	status := make([]byte, 4)
	if _, err := io.ReadFull(conn, status); err != nil {
		return err
	}
	if string(status) != "OKAY" {
		return fmt.Errorf("ADB 设备监听不可用")
	}
	for {
		header := make([]byte, 4)
		if _, err := io.ReadFull(conn, header); err != nil {
			return err
		}
		n, err := strconv.ParseUint(string(header), 16, 16)
		if err != nil {
			return err
		}
		body := make([]byte, int(n))
		if _, err = io.ReadFull(conn, body); err != nil {
			return err
		}
		changed(parseADBDevices(string(body)))
	}
}
func parseADBDevices(out string) []device {
	var result []device
	for _, line := range strings.Split(out, "\n") {
		m := adbDeviceLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) == 0 {
			continue
		}
		d := device{Serial: m[1], State: m[2], Kind: "ADB"}
		for _, p := range strings.Fields(m[3]) {
			if strings.HasPrefix(p, "model:") {
				d.Model = strings.TrimPrefix(p, "model:")
			}
		}
		result = append(result, d)
	}
	return result
}

// mergeADBEvent never promotes a new or reappearing transport to usable before identity verification.
func mergeADBEvent(current, tracked []device) []device {
	bySerial := map[string]device{}
	for _, d := range tracked {
		bySerial[d.Serial] = d
	}
	seen := map[string]bool{}
	next := make([]device, 0, len(current)+len(tracked))
	for _, old := range current {
		if old.Kind != "ADB" {
			next = append(next, old)
			continue
		}
		d, ok := bySerial[old.Serial]
		if !ok {
			if old.Hardware != "" {
				old.State = "offline"
				next = append(next, old)
			}
			continue
		}
		seen[old.Serial] = true
		if d.State != "device" {
			old.State = d.State
		} else if old.State != "device" {
			old.State = "verifying"
		}
		next = append(next, old)
	}
	for _, d := range tracked {
		if !seen[d.Serial] {
			if d.State == "device" {
				d.State = "verifying"
			}
			next = append(next, d)
		}
	}
	return next
}
func fastbootDevices(ds []device) []device {
	var out []device
	for _, d := range ds {
		if d.Kind == "Fastboot" {
			out = append(out, d)
		}
	}
	return out
}
func sameFastboot(current, latest []device) bool {
	old := fastbootDevices(current)
	if len(old) != len(latest) {
		return false
	}
	for i := range old {
		if old[i] != latest[i] {
			return false
		}
	}
	return true
}
func mergeFastboot(current, latest []device) []device {
	out := make([]device, 0, len(current)+len(latest))
	for _, d := range current {
		if d.Kind != "Fastboot" {
			out = append(out, d)
		}
	}
	return append(out, latest...)
}
