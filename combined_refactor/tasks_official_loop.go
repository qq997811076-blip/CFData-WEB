package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type officialScanCompletion struct { result *ScanResult }

func scanOfficialAutoBatch(ctx context.Context, session *appSession, remaining []string, base, total, maxWorkers, port, delay, limit int, scanMode string) ([]ScanResult, int, bool) {
	if limit <= 0 { limit = 1000 }
	if maxWorkers <= 0 { maxWorkers = 1 }
	if maxWorkers > len(remaining) { maxWorkers = len(remaining) }
	if maxWorkers <= 0 || len(remaining) == 0 { return nil, 0, false }
	jobs := make(chan int)
	completed := make(chan officialScanCompletion, maxWorkers)
	var workers sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for idx := range jobs {
				var result *ScanResult
				if ctx.Err() == nil {
					if scanMode == scanModeHTTPing { result, _, _ = scanOfficialHTTP(ctx, remaining[idx], port, delay) } else { result, _, _ = scanOfficialIP(ctx, remaining[idx], port, delay) }
				}
				completed <- officialScanCompletion{result: result}
			}
		}()
	}
	results := make([]ScanResult, 0, min(limit, len(remaining)))
	next, inFlight, completedCount := 0, 0, 0
	canceled := false
	for {
		for next < len(remaining) && len(results)+inFlight < limit && inFlight < maxWorkers && ctx.Err() == nil {
			jobs <- next
			next++
			inFlight++
		}
		if inFlight == 0 {
			if next >= len(remaining) || len(results) >= limit || ctx.Err() != nil { break }
			continue
		}
		item := <-completed
		inFlight--
		completedCount++
		if item.result != nil {
			results = append(results, *item.result)
			session.sendWSMessage("scan_result", *item.result)
		}
		session.sendWSMessage("scan_progress", map[string]interface{}{"current": min(base+completedCount, total), "total": total})
		if ctx.Err() != nil { canceled = true }
		if canceled && inFlight == 0 { break }
		if len(results) >= limit && inFlight == 0 { break }
	}
	close(jobs)
	for inFlight > 0 {
		item := <-completed
		inFlight--
		completedCount++
		if item.result != nil {
			results = append(results, *item.result)
			session.sendWSMessage("scan_result", *item.result)
		}
		session.sendWSMessage("scan_progress", map[string]interface{}{"current": min(base+completedCount, total), "total": total})
	}
	workers.Wait()
	if ctx.Err() != nil { canceled = true }
	sort.Slice(results, func(i, j int) bool { return results[i].TCPDuration < results[j].TCPDuration })
	return results, next, canceled
}

func officialDataCenters(results []ScanResult) []DataCenterInfo {
	byDC := make(map[string]*DataCenterInfo)
	for _, result := range results {
		if _, ok := byDC[result.DataCenter]; !ok {
			byDC[result.DataCenter] = &DataCenterInfo{DataCenter: result.DataCenter, DCCountry: result.DCCountry, City: result.City, MinLatency: int(result.TCPDuration / time.Millisecond)}
		}
		info := byDC[result.DataCenter]
		info.IPCount++
		latency := int(result.TCPDuration / time.Millisecond)
		if latency < info.MinLatency { info.MinLatency = latency }
	}
	out := make([]DataCenterInfo, 0, len(byDC))
	for _, info := range byDC { out = append(out, *info) }
	sort.Slice(out, func(i, j int) bool { return out[i].MinLatency < out[j].MinLatency })
	return out
}

func runOfficialDetailedBatch(ctx context.Context, session *appSession, scanResults []ScanResult, selectedDC string, port, delay int, scanMode string) ([]TestResult, bool) {
	selected := make([]ScanResult, 0, len(scanResults))
	for _, scan := range scanResults { if selectedDC == "" || strings.EqualFold(scan.DataCenter, selectedDC) { selected = append(selected, scan) } }
	if len(selected) == 0 { return nil, ctx.Err() != nil }
	results := make([]TestResult, 0, len(selected))
	var mu sync.Mutex
	session.sendWSMessage("test_progress", map[string]interface{}{"current": 0, "total": len(selected)})
	canceled := runBoundedWorkers(ctx, len(selected), 50, 5, func(current, total int) {
		session.sendWSMessage("test_progress", map[string]interface{}{"current": current, "total": total})
	}, func(idx int) {
		if ctx.Err() != nil { return }
		scan := selected[idx]
		result, _, _ := testIPLatency(ctx, scan.IP, port, delay, scanMode)
		if result == nil || ctx.Err() != nil { return }
		result.Port = port
		result.DataCenter, result.DCCountry, result.Region, result.City = scan.DataCenter, scan.DCCountry, scan.Region, scan.City
		result.Speed = "未测速"
		mu.Lock(); results = append(results, *result); mu.Unlock()
		session.sendWSMessage("test_result", *result)
	})
	sortOfficialTestResults(results)
	return results, canceled || ctx.Err() != nil
}

func runOfficialAutoLoop(ctx context.Context, session *appSession, ipType, scanThreads, port, delay int, scanMode string, scanLimit int, targetDC string, speedPort int, speedURL string, speedMin float64, speedLimit, speedThreads int) {
	if scanLimit <= 0 { scanLimit = 1000 }
	if speedPort <= 0 { speedPort = 443 }
	if speedMin <= 0 { speedMin = 0.1 }
	if speedThreads <= 0 { speedThreads = 1 }
	filename, apiURL := "ips-v4.txt", "https://www.baipiao.eu.org/cloudflare/ips-v4"
	if ipType == 6 { filename, apiURL = "ips-v6.txt", "https://www.baipiao.eu.org/cloudflare/ips-v6" }
	content, err := getIPListContent(filename, apiURL)
	if err != nil {
		session.sendWSMessage("error", err.Error())
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "totalQualified": 0, "limit": speedLimit, "rateLimited": false})
		return
	}
	ipList, err := parseIPList(content)
	if err != nil {
		session.sendWSMessage("error", "解析 IP 列表失败: "+err.Error())
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "totalQualified": 0, "limit": speedLimit, "rateLimited": false})
		return
	}
	if ipType == 6 { ipList = getRandomIPv6s(ipList) } else { ipList = getRandomIPv4s(ipList) }
	total := len(ipList)
	if total == 0 {
		session.sendWSMessage("error", "官方 IP 源没有解析出可扫描地址")
		session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": 0, "totalQualified": 0, "limit": speedLimit, "rateLimited": false})
		return
	}
	session.scanMutex.Lock(); session.scanResults = []ScanResult{}; session.scanMutex.Unlock()
	session.testMutex.Lock(); session.testResults = []TestResult{}; session.testMutex.Unlock()
	session.sendWSMessage("log", fmt.Sprintf("官方自动循环已开启：IP 源 %d 个，每轮扫描合格上限 %d，测速达标目标 %d", total, scanLimit, speedLimit))
	session.sendWSMessage("scan_progress", map[string]interface{}{"current": 0, "total": total})
	allScans := make([]ScanResult, 0)
	allTests := make([]TestResult, 0)
	cursor, qualified, round := 0, 0, 0
	rateLimitedLastRound, canceled := false, false
	for cursor < total && qualified < speedLimit {
		if ctx.Err() != nil { canceled = true; break }
		round++
		start := cursor
		roundScans, consumed, scanCanceled := scanOfficialAutoBatch(ctx, session, ipList[cursor:], cursor, total, scanThreads, port, delay, scanLimit, scanMode)
		if consumed <= 0 { canceled = ctx.Err() != nil; break }
		cursor += consumed
		allScans = append(allScans, roundScans...)
		session.sendWSMessage("log", fmt.Sprintf("官方自动循环第 %d 轮：扫描处理 %d 个，合格 %d 个，剩余源 IP %d 个，测速还差 %d 个", round, cursor-start, len(roundScans), total-cursor, speedLimit-qualified))
		if len(roundScans) > 0 && ctx.Err() == nil {
			dc := strings.TrimSpace(targetDC)
			if dc == "" { dc = pickBestDataCenter(roundScans) }
			roundTests, testCanceled := runOfficialDetailedBatch(ctx, session, roundScans, dc, speedPort, delay, scanMode)
			if len(roundTests) > 0 {
				remainingTarget := speedLimit - qualified
				if remainingTarget > 0 && ctx.Err() == nil {
					updated, _ := runOfficialSpeedTestsCore(ctx, roundTests, speedPort, remainingTarget, speedMin, speedURL, speedThreads, nil, func() {
						rateLimitedLastRound = true
						session.sendWSMessage("log", speedRateLimitMessage)
					})
					roundTests = updated
				}
				for i := range roundTests { if strings.TrimSpace(roundTests[i].Speed) == "" { roundTests[i].Speed = "未测速" } }
				allTests = append(allTests, roundTests...)
				session.testMutex.Lock(); session.testResults = append([]TestResult(nil), allTests...); session.testMutex.Unlock()
				qualified = countOfficialQualifiedResults(allTests, speedMin)
				session.sendWSMessage("official_speed_progress", map[string]interface{}{"current": min(qualified, speedLimit), "total": speedLimit, "qualified": qualified, "limit": speedLimit})
				session.sendWSMessage("log", fmt.Sprintf("官方自动循环第 %d 轮完成：累计测速达标 %d/%d", round, qualified, speedLimit))
			}
			if testCanceled { canceled = true }
		}
		if scanCanceled || ctx.Err() != nil { canceled = true; break }
		if qualified >= speedLimit { break }
	}
	sort.Slice(allScans, func(i, j int) bool { return allScans[i].TCPDuration < allScans[j].TCPDuration })
	sortOfficialTestResults(allTests)
	session.scanMutex.Lock(); session.scanResults = append([]ScanResult(nil), allScans...); session.scanMutex.Unlock()
	session.testMutex.Lock(); session.testResults = append([]TestResult(nil), allTests...); session.testMutex.Unlock()
	if len(allScans) > 0 { session.sendWSMessage("scan_complete_wait_dc", officialDataCenters(allScans)) }
	session.sendWSMessage("test_complete", allTests)
	for _, result := range allTests {
		if strings.TrimSpace(result.Speed) != "" && result.Speed != "未测速" { session.sendWSMessage("speed_test_result", map[string]string{"ip": result.IP, "speed": result.Speed}) }
	}
	qualified = countOfficialQualifiedResults(allTests, speedMin)
	finishedByTarget := qualified >= speedLimit
	finalRateLimited := rateLimitedLastRound && !finishedByTarget && cursor < total
	message := fmt.Sprintf("官方自动循环结束：已扫描 %d/%d 个 IP，测速达标 %d/%d 个", cursor, total, qualified, speedLimit)
	if finishedByTarget { message += "，已达到目标" } else if canceled { message += "，任务已终止" } else if cursor >= total { message += "，IP 源已耗尽" }
	session.sendWSMessage("log", message)
	session.sendWSMessage("official_speed_complete", map[string]interface{}{"qualified": qualified, "totalQualified": qualified, "limit": speedLimit, "rateLimited": finalRateLimited})
}
