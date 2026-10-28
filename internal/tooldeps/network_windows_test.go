package tooldeps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyConfigAndDirect(t *testing.T) {
	for _, bad := range []string{"proxy.local:80", "127.0.0.1:0", "http://127.0.0.1:80", "127.0.0.1:99999"} {
		if (NetworkConfig{Address: bad}).Validate() == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))
	defer srv.Close()
	client, err := (NetworkConfig{}).Client()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
func TestHTTPProxyUsed(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); fmt.Fprint(w, "proxy") }))
	defer srv.Close()
	address := strings.TrimPrefix(srv.URL, "http://")
	client, err := (NetworkConfig{Protocol: "HTTP", Address: address}).Client()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://example.test/file")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if requests.Load() != 1 {
		t.Fatal("proxy was not used")
	}
}
func TestSOCKS5ProxyUsed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		r := bufio.NewReader(c)
		head := make([]byte, 2)
		if _, e = io.ReadFull(r, head); e != nil {
			done <- e
			return
		}
		methods := make([]byte, int(head[1]))
		if _, e = io.ReadFull(r, methods); e != nil {
			done <- e
			return
		}
		if _, e = c.Write([]byte{5, 0}); e != nil {
			done <- e
			return
		}
		request := make([]byte, 4)
		if _, e = io.ReadFull(r, request); e != nil {
			done <- e
			return
		}
		size := 0
		switch request[3] {
		case 1:
			size = 4
		case 4:
			size = 16
		case 3:
			b, e := r.ReadByte()
			if e != nil {
				done <- e
				return
			}
			size = int(b)
		default:
			done <- errors.New("invalid address")
			return
		}
		tail := make([]byte, size+2)
		if _, e = io.ReadFull(r, tail); e != nil {
			done <- e
			return
		}
		if _, e = c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 1}); e != nil {
			done <- e
			return
		}
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				done <- e
				return
			}
			if line == "\r\n" {
				break
			}
		}
		_, e = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
		done <- e
	}()
	client, err := (NetworkConfig{Protocol: "SOCKS5", Address: listener.Addr().String()}).Client()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://example.test/file")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
func TestDownloadReportsAndCancels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello") }))
	defer srv.Close()
	var progress []Progress
	var out strings.Builder
	_, err := Download(DownloadOptions{Progress: func(p Progress) { progress = append(progress, p) }}, "组件", srv.URL, &out, 100)
	if err != nil || out.String() != "hello" || len(progress) < 2 || progress[len(progress)-1].Current != 5 || progress[len(progress)-1].Total != 5 {
		t.Fatalf("%s %+v %v", out.String(), progress, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Download(DownloadOptions{Context: ctx}, "组件", srv.URL, io.Discard, 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSystemProxyParsing(t *testing.T) {
	u, _ := url.Parse("https://example.com/file")
	p, err := parseSystemProxy("http=127.0.0.1:8000;https=127.0.0.1:9000", u)
	if err != nil || p.Host != "127.0.0.1:9000" {
		t.Fatalf("%v %v", p, err)
	}
	if !bypassProxy("*.example.com;example.com", u) {
		t.Fatal("bypass missing")
	}
	p, err = parseSystemProxy("DIRECT", u)
	if err != nil || p != nil {
		t.Fatal("direct failed")
	}
}
