package tooldeps

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type NetworkConfig struct {
	Protocol, Address string
	System            bool
}
type Progress struct {
	Name           string
	Current, Total int64
}
type DownloadOptions struct {
	Context  context.Context
	Client   *http.Client
	Progress func(Progress)
}

func (c NetworkConfig) Validate() error {
	if c.Protocol != "" && c.Protocol != "HTTP" && c.Protocol != "SOCKS5" {
		return errors.New("代理协议无效")
	}
	if c.System {
		return nil
	}
	if c.Address == "" {
		return nil
	}
	h, p, e := net.SplitHostPort(c.Address)
	if e != nil || net.ParseIP(h) == nil {
		return errors.New("请输入 IP:端口")
	}
	n, e := strconv.Atoi(p)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("代理端口无效")
	}
	return nil
}
func (c NetworkConfig) Client() (*http.Client, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if c.System {
		transport.Proxy = systemProxy
	} else if c.Address != "" {
		scheme := "http"
		if c.Protocol == "SOCKS5" {
			scheme = "socks5"
		}
		proxyURL := &url.URL{Scheme: scheme, Host: c.Address}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: transport}, nil
}
func (o DownloadOptions) context() context.Context {
	if o.Context != nil {
		return o.Context
	}
	return context.Background()
}
func (o DownloadOptions) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return http.DefaultClient
}

type progressWriter struct {
	name           string
	current, total int64
	callback       func(Progress)
	last           time.Time
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.current += int64(len(p))
	if w.callback != nil && (w.last.IsZero() || time.Since(w.last) >= 100*time.Millisecond) {
		w.last = time.Now()
		w.callback(Progress{w.name, w.current, w.total})
	}
	return len(p), nil
}
func Download(o DownloadOptions, name, source string, dst io.Writer, limit int64) (string, error) {
	req, e := http.NewRequestWithContext(o.context(), http.MethodGet, source, nil)
	if e != nil {
		return "", e
	}
	resp, e := o.client().Do(req)
	if e != nil {
		return "", e
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败：%s", resp.Status)
	}

	h := sha256.New()
	w := &progressWriter{name: name, total: resp.ContentLength, callback: o.Progress}
	if o.Progress != nil {
		o.Progress(Progress{name, 0, resp.ContentLength})
	}
	n, e := io.Copy(io.MultiWriter(dst, h, w), io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return "", e
	}
	if n > limit {
		return "", errors.New("下载文件过大")
	}
	if o.Progress != nil {
		o.Progress(Progress{name, n, resp.ContentLength})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
