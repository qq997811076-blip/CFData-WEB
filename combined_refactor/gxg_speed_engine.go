package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// gxgSpeedEngine 将 gxg myspeed.go 的单 IP 下载测速规则移植为可由 WEB/非标接口共用的引擎。
// 上层仍负责现有 WebSocket、任务取消、结果回写、筛选和导出接口。
const (
	gxgTestDuration     = 5 * time.Second
	gxgIdleTimeout      = 3 * time.Second
	gxgReadBufferSize   = 32 * 1024
	gxgMinDownloadBytes = int64(64 * 1024)
	gxgMinMeasureTime   = 100 * time.Millisecond
	gxgConnectTimeout   = 5 * time.Second
)

const gxgDefaultDownloadLimit int64 = 50 * 1024 * 1024

type gxgReadResult struct {
	n   int
	err error
}

type gxgSpeedMeasurement struct {
	Bytes int64
	Used  time.Duration
	MBps  float64
}

func gxgDownloadTest(ctx context.Context, ip string, port int, customURL, defaultScheme string) (gxgSpeedMeasurement, string) {
	var result gxgSpeedMeasurement
	if ctx == nil {
		ctx = context.Background()
	}
	if port <= 0 || port > 65535 {
		return result, "测速端口无效"
	}
	if defaultScheme != "https" {
		defaultScheme = "http"
	}
	parsedURL, err := parseSpeedTestURL(customURL, defaultScheme)
	if err != nil {
		return result, "测速地址解析失败: " + err.Error()
	}
	if parsedURL.Hostname() == "" {
		return result, "测速地址缺少主机名"
	}

	// 与参考实现一致：5 秒总期限从发起 HTTP 请求前开始，覆盖连接、TLS、响应头和下载。
	testCtx, cancel := context.WithTimeout(ctx, gxgTestDuration)
	defer cancel()

	transport := &http.Transport{
		DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: gxgConnectTimeout}
			return dialer.DialContext(c, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		},
		TLSHandshakeTimeout: gxgTestDuration,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		DisableCompression:  true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   gxgTestDuration,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return result, "测速请求构建失败: " + err.Error()
	}
	req.Host = parsedURL.Host
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", gxgDefaultDownloadLimit-1))

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return result, "测速任务已终止"
		}
		return result, "测速请求失败: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		if resp.StatusCode == http.StatusTooManyRequests {
			return result, "测速失败（速率限制）"
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return result, fmt.Sprintf("测速失败（HTTP %d）", resp.StatusCode)
	}

	// 与 gxg 一致：速度计时从有效响应头返回后开始；连接/握手时间不计入下载速度。
	start := time.Now()
	var firstData, lastData time.Time
	var total int64
	buf := make([]byte, gxgReadBufferSize)

	for time.Since(start) < gxgTestDuration && total < gxgDefaultDownloadLimit {
		b := buf
		if left := gxgDefaultDownloadLimit - total; int64(len(b)) > left {
			b = b[:int(left)]
		}
		ch := make(chan gxgReadResult, 1)
		go func(readBuf []byte) {
			n, readErr := resp.Body.Read(readBuf)
			ch <- gxgReadResult{n: n, err: readErr}
		}(b)

		wait := gxgIdleTimeout
		if !lastData.IsZero() {
			wait -= time.Since(lastData)
		}
		if wait <= 0 {
			cancel()
			break
		}
		timer := time.NewTimer(wait)
		select {
		case chunk := <-ch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if chunk.n > 0 {
				now := time.Now()
				if firstData.IsZero() {
					firstData = now
				}
				lastData = now
				total += int64(chunk.n)
			}
			if chunk.err != nil {
				if chunk.err != io.EOF && total == 0 {
					return result, "测速下载失败: " + chunk.err.Error()
				}
				goto finished
			}
			if chunk.n == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		case <-timer.C:
			cancel()
			goto finished
		case <-testCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			goto finished
		}
	}

finished:
	if ctx.Err() != nil {
		return result, "测速任务已终止"
	}
	if total < gxgMinDownloadBytes {
		return result, "有效下载数据不足 64 KiB"
	}
	used := lastData.Sub(start)
	if used < gxgMinMeasureTime {
		used = gxgMinMeasureTime
	}
	result.Bytes = total
	result.Used = used
	result.MBps = float64(total) / used.Seconds() / 1024 / 1024
	return result, ""
}

// gxgCheckIP 是参考实现的连通性/延迟检测阶段，供需要独立预检的测速流程复用。
// 现有 WEB 的扫描阶段继续通过原接口输出结果，避免改变页面协议。
func gxgCheckIP(ctx context.Context, ip string, port int) (time.Duration, bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	testCtx, cancel := context.WithTimeout(ctx, gxgConnectTimeout)
	defer cancel()
	start := time.Now()
	target := "https://speed.cloudflare.com/cdn-cgi/trace"
	transport := &http.Transport{
		DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: gxgConnectTimeout}
			return dialer.DialContext(c, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		},
		TLSHandshakeTimeout: gxgConnectTimeout,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   gxgConnectTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, target, nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("User-Agent", "IP-HTTP-Checker/1.0")
	req.Header.Set("Connection", "close")
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return latency, false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return latency, true
}

func gxgSchemeFromTLS(enabled bool) string {
	if enabled {
		return "https"
	}
	return "http"
}

func gxgNormalizeURLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	return u.Host
}
