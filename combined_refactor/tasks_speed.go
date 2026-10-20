package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func isTLSPort(port int) bool {
	return port == 443 || port == 2053 || port == 2083 || port == 2087 || port == 2096 || port == 8443
}

func runWindowedSpeedTest(ctx context.Context, ip string, port int, customURL string) (float64, string) {
	// 参考 gxg myspeed.go：请求阶段和下载阶段共用 5 秒总期限；并发由固定 worker 池调度。
	const (
		testDuration     = 5 * time.Second
		idleTimeout      = 3 * time.Second
		readBufferSize   = 32 * 1024
		minDownloadBytes = 64 * 1024
		minMeasureTime   = 100 * time.Millisecond
	)
	const downloadLimit int64 = 50 * 1024 * 1024

	scheme := "http"
	if isTLSPort(port) {
		scheme = "https"
	}

	parsedURL, err := parseSpeedTestURL(customURL, scheme)
	if err != nil {
		return 0, "URL解析错误: " + err.Error()
	}

	testCtx, cancel := context.WithTimeout(ctx, testDuration)
	defer cancel()

	transport := &http.Transport{
		DialContext: func(c context.Context, network, addr string) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: 5 * time.Second}
			return dialer.DialContext(c, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		},
		TLSHandshakeTimeout: testDuration,
		TLSClientConfig:     tlsConfigWithRootCAs(parsedURL.Hostname()),
		DisableCompression:  true,
	}
	client := http.Client{
		Transport: wrapDebugTransport("official-speed", transport),
		Timeout:   testDuration,
	}

	req, err := http.NewRequestWithContext(testCtx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return 0, "请求构造错误"
	}
	req.Host = parsedURL.Host
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", downloadLimit-1))

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, "测速任务已终止"
		}
		return 0, "连接错误"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return 0, formatSpeedHTTPFailure(resp.StatusCode)
	}

	// 与参考实现一致：下载测速计时从收到有效响应头后开始。
	measuredStart := time.Now()
	var totalBytes int64
	var lastData time.Time
	buf := make([]byte, readBufferSize)

	type readChunk struct {
		n   int
		err error
	}

	for time.Since(measuredStart) < testDuration && totalBytes < downloadLimit {
		b := buf
		if remaining := downloadLimit - totalBytes; int64(len(b)) > remaining {
			b = b[:int(remaining)]
		}
		ch := make(chan readChunk, 1)
		go func(readBuf []byte) {
			n, readErr := resp.Body.Read(readBuf)
			ch <- readChunk{n: n, err: readErr}
		}(b)

		wait := idleTimeout
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
				totalBytes += int64(chunk.n)
				lastData = time.Now()
			}
			if chunk.err != nil {
				if chunk.err != io.EOF && totalBytes == 0 {
					return 0, "连接错误"
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
		return 0, "测速任务已终止"
	}
	if totalBytes < minDownloadBytes {
		return 0, "有效下载数据不足 64 KiB"
	}

	used := lastData.Sub(measuredStart)
	if used < minMeasureTime {
		used = minMeasureTime
	}
	return float64(totalBytes) / used.Seconds() / 1024 / 1024, ""
}

func formatSpeedHTTPFailure(statusCode int) string {
	if statusCode == http.StatusTooManyRequests {
		return "测速失败（速率限制）"
	}
	return "测速失败"
}

func runSpeedTest(ctx context.Context, session *appSession, ip string, port int, customURL string) {
	session.sendWSMessage("log", fmt.Sprintf("开始对 IP %s 端口 %d 进行测速...", ip, port))

	speedMB, speedErr := runWindowedSpeedTest(ctx, ip, port, customURL)
	if speedErr != "" {
		if ctx.Err() != nil {
			return
		}
		session.sendWSMessage("speed_test_result", map[string]string{"ip": ip, "speed": speedErr})
		session.sendWSMessage("log", "测速失败: "+speedErr)
		return
	}
	if ctx.Err() != nil {
		return
	}
	speedStr := fmt.Sprintf("%.2fMB/s", speedMB)

	session.sendWSMessage("speed_test_result", map[string]string{"ip": ip, "speed": speedStr})
	session.sendWSMessage("log", fmt.Sprintf("IP %s 测速完成: %s", ip, speedStr))
}

const speedRateLimitMessage = "检测到触发速率限制，请稍后重试或更换网络环境"

func isSpeedRateLimited(speedErr string) bool {
	return strings.Contains(speedErr, "速率限制")
}

func runOfficialSpeedTestsCore(ctx context.Context, results []TestResult, port int, limit int, concurrency int, speedMinMB float64, customURL string, onResult func(current, total, qualified int, result TestResult), onRateLimited func()) ([]TestResult, []TestResult) {
	capacity := len(results)
	if limit > 0 && limit < capacity {
		capacity = limit
	}
	qualified := make([]TestResult, 0, capacity)
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > 100 {
		concurrency = 100
	}
	if len(results) == 0 {
		return results, qualified
	}
	if concurrency > len(results) {
		concurrency = len(results)
	}

	// 采用 gxg 的固定 worker 池并发模型；结果回调仍由协调 goroutine 顺序执行，
	// 避免 WebSocket 进度、合格数量及速率限制状态发生并发竞争。
	type speedDone struct {
		idx     int
		speedMB float64
		err     string
	}
	jobs := make(chan int, concurrency)
	done := make(chan speedDone, concurrency)
	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for idx := range jobs {
				speedMB, speedErr := runWindowedSpeedTest(ctx, results[idx].IP, port, customURL)
				done <- speedDone{idx: idx, speedMB: speedMB, err: speedErr}
			}
		}()
	}

	next, inFlight, completed := 0, 0, 0
	rateLimitedCount := 0
	stopped := false
	ctxDone := ctx.Done()

	schedule := func() {
		for !stopped && ctx.Err() == nil && next < len(results) &&
			inFlight < concurrency && (limit <= 0 || len(qualified)+inFlight < limit) {
			jobs <- next
			next++
			inFlight++
		}
	}
	schedule()

	for inFlight > 0 {
		if ctx.Err() != nil {
			stopped = true
			ctxDone = nil
		}
		select {
		case <-ctxDone:
			stopped = true
			ctxDone = nil
		case item := <-done:
			inFlight--
			completed++
			if ctx.Err() != nil {
				continue
			}
			if item.err != "" {
				results[item.idx].Speed = item.err
				if isSpeedRateLimited(item.err) {
					rateLimitedCount++
					if rateLimitedCount >= 3 && !stopped {
						stopped = true
						if onRateLimited != nil {
							onRateLimited()
						}
					}
				}
			} else {
				results[item.idx].Speed = fmt.Sprintf("%.2fMB/s", item.speedMB)
				if item.speedMB >= speedMinMB {
					qualified = append(qualified, results[item.idx])
				}
			}
			if onResult != nil {
				onResult(completed, len(results), len(qualified), results[item.idx])
			}
			schedule()
		}
	}

	close(jobs)
	workers.Wait()
	return results, qualified
}

func runOfficialSpeedBatch(ctx context.Context, session *appSession, port int, customURL string, speedLimit int, speedConcurrency int, speedMin float64, fallbackResults []TestResult, skipTested bool) {
	if port <= 0 {
		port = 443
	}
	if speedLimit <= 0 {
		session.sendWSMessage("log", "官方批量测速已关闭（测速上限 0）")
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "limit": speedLimit, "rateLimited": false})
		return
	}
	if speedMin <= 0 {
		speedMin = 0.1
	}

	session.testMutex.Lock()
	results := append([]TestResult(nil), session.testResults...)
	session.testMutex.Unlock()
	if len(results) == 0 && len(fallbackResults) > 0 {
		results = append([]TestResult(nil), fallbackResults...)
		session.testMutex.Lock()
		session.testResults = append([]TestResult(nil), results...)
		session.testMutex.Unlock()
	}
	if len(results) == 0 {
		session.sendWSMessage("log", "没有可用的详细测试结果，跳过官方批量测速")
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "limit": speedLimit, "rateLimited": false})
		return
	}
	if skipTested {
		pending := results[:0]
		for _, result := range results {
			speedText := strings.TrimSpace(result.Speed)
			if speedText == "" || speedText == "未测速" {
				pending = append(pending, result)
			}
		}
		results = pending
		if len(results) == 0 {
			session.sendWSMessage("log", "没有未测速的详细测试结果，跳过继续测速")
			session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "limit": speedLimit, "rateLimited": false})
			return
		}
	}
	sortOfficialTestResults(results)

	if speedConcurrency <= 0 { speedConcurrency = 1 }; if speedConcurrency > 100 { speedConcurrency = 100 }
	session.sendWSMessage("log", fmt.Sprintf("开始测速：%d 条记录，并发=%d，目标上限=%d，测速阈值=%.2fMB/s", len(results), speedConcurrency, speedLimit, speedMin))
	session.sendWSMessage("official_speed_progress", map[string]interface{}{"current": 0, "total": len(results), "qualified": 0, "limit": speedLimit})
	rateLimited := false
	updated, qualified := runOfficialSpeedTestsCore(ctx, results, port, speedLimit, speedConcurrency, speedMin, customURL, func(current, total, qualified int, result TestResult) {
		session.sendWSMessage("speed_test_result", map[string]string{"ip": result.IP, "speed": result.Speed})
		session.sendWSMessage("official_speed_progress", map[string]interface{}{"current": current, "total": total, "qualified": qualified, "limit": speedLimit})
	}, func() {
		rateLimited = true
		session.sendWSMessage("log", speedRateLimitMessage)
	})

	session.testMutex.Lock()
	byIP := make(map[string]string, len(updated))
	for _, result := range updated {
		byIP[result.IP] = result.Speed
	}
	for idx := range session.testResults {
		if speed, ok := byIP[session.testResults[idx].IP]; ok {
			session.testResults[idx].Speed = speed
		}
	}
	totalQualified := countOfficialQualifiedResults(session.testResults, speedMin)
	session.testMutex.Unlock()
	if ctx.Err() != nil {
		session.sendWSMessage("log", "官方批量测速已终止")
	} else if rateLimited {
		session.sendWSMessage("log", fmt.Sprintf("官方批量测速已因速率限制停止，达标 %d/%d，总达标 %d", len(qualified), speedLimit, totalQualified))
	} else {
		session.sendWSMessage("log", fmt.Sprintf("官方批量测速完成，达标 %d/%d，总达标 %d", len(qualified), speedLimit, totalQualified))
	}
	if ctx.Err() == nil {
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": len(qualified), "totalQualified": totalQualified, "limit": speedLimit, "rateLimited": rateLimited})
	}
}

func countOfficialQualifiedResults(results []TestResult, speedMin float64) int {
	count := 0
	for _, result := range results {
		if speed, ok := parseSpeedMBForSort(result.Speed); ok && speed >= speedMin {
			count++
		}
	}
	return count
}
