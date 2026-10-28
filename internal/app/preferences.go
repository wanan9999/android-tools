package app

type preferences struct {
	Audio   bool   `json:"audio"`
	Size    string `json:"size"`
	FPS     string `json:"fps"`
	Bitrate string `json:"bitrate,omitempty"`
}

func defaultPreferences() preferences {
	return preferences{Audio: true, Size: "1280", FPS: "30", Bitrate: "8 Mbps"}
}
func validBitrate(v string) bool {
	switch v {
	case "8 Mbps", "12 Mbps", "16 Mbps", "24 Mbps":
		return true
	}
	return false
}
func (p preferences) valid() bool {
	return (p.Size == "720" || p.Size == "1280" || p.Size == "1920" || p.Size == "2560") && (p.FPS == "15" || p.FPS == "30" || p.FPS == "60") && validBitrate(p.Bitrate)
}
func (a *app) savePreferences() {
	if a.preferencesQueue == nil {
		return
	}
	p := preferences{Audio: a.audio, Size: a.size, FPS: a.fps, Bitrate: a.bitrate}
	select {
	case a.preferencesQueue <- p:
	default:
		select {
		case <-a.preferencesQueue:
		default:
		}
		a.preferencesQueue <- p
	}
}
func (a *app) writePreferencesLoop() {
	defer close(a.preferencesDone)
	for p := range a.preferencesQueue {
		if err := a.manager.registry.savePreferences(p); err != nil {
			a.win.Update(func() { a.message = "设置保存失败：" + err.Error() })
		}
	}
}
