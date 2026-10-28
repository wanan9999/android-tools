package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMeasureProxyLatencyReusesConnection(t *testing.T) {
	var remotes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remotes = append(remotes, r.RemoteAddr)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := server.Client()
	defer client.CloseIdleConnections()
	if _, err := measureProxyLatency(context.Background(), client, server.URL); err != nil {
		t.Fatal(err)
	}
	if len(remotes) != 2 || remotes[0] != remotes[1] {
		t.Fatalf("connection not reused: %v", remotes)
	}
}

func TestMeasureProxyLatencyFailures(t *testing.T) {
	for _, fail := range []int{1, 2} {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls >= fail {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			client := server.Client()
			defer client.CloseIdleConnections()
			if _, err := measureProxyLatency(context.Background(), client, server.URL); err == nil {
				t.Fatal("bad status accepted")
			}
			want := 1
			if fail == 2 {
				want = 3
			}
			if calls != want {
				t.Fatalf("got %d requests, want %d", calls, want)
			}
		})
	}
}

func TestNetworkSettingsLayout(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {960, 680}} {
		for _, latency := range []string{"", "…", "-1", "123 ms"} {
			a := &app{engine: newEngine(), page: "settings", proxyProtocol: "SOCKS5", proxyAddress: "127.0.0.1:10808", proxyLatency: latency}
			tt := ui.NewTester(a.view, size[0], size[1])
			for _, label := range []string{"使用系统代理", "代理协议", "代理地址", "测试延迟"} {
				if _, ok := tt.Find(label); !ok {
					t.Fatalf("%v %q: missing %s", size, latency, label)
				}
			}
		}
	}
}

func TestNetworkChangedInvalidatesTest(t *testing.T) {
	a := &app{proxyTesting: true, proxyLatency: "123 ms", proxyError: "连接成功"}
	a.networkChanged()
	if a.proxyTesting || a.proxyLatency != "" || a.proxyError != "" || a.proxyRevision != 1 {
		t.Fatal("network change did not invalidate old result")
	}
}
