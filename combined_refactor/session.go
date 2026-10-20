package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var globalRunningTasks int32
var globalTaskMutex sync.Mutex
var activeTaskNamespaces = map[string]int{}
var backgroundTaskMutex sync.Mutex
var backgroundTaskSessions = map[string]*appSession{}

func anyTaskRunning() bool {
	return atomic.LoadInt32(&globalRunningTasks) > 0
}

func normalizeTaskNamespace(value string) string {
	if value == "nsb" {
		return "nsb"
	}
	return "official"
}

func safeGo(label string, session *appSession, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("%s panic: %v", label, r)
				fmt.Printf("%s\n%s\n", msg, debug.Stack())
				recordProgramDebugError("panic", fmt.Sprintf("%s\n%s", msg, debug.Stack()))
				if session != nil {
					session.sendWSMessage("error", "内部错误: "+msg)
				}
			}
		}()
		fn()
	}()
}

func (s *appSession) sendWSMessage(msgType string, data interface{}) {
	updatedBackground := false
	if s.emit == nil {
		s.updateBackgroundSnapshotFromMessage(msgType, data)
		updatedBackground = s.isBackgroundTask()
	}
	if updatedBackground && msgType != "background_task_update" && msgType != "background_task_found" && msgType != "background_task_enabled" {
		s.sendWSMessageDirect("background_task_update", s.backgroundSummary())
	}
	s.sendWSMessageDirect(msgType, data)
}

func (s *appSession) sendWSMessageDirect(msgType string, data interface{}) {
	if s.emit != nil {
		if msgType == "error" || msgType == "github_upload_error" || msgType == "edgetunnel_upload_error" {
			recordProgramDebugError(msgType, data)
		} else if msgType == "log" || msgType == "github_upload_status" || msgType == "edgetunnel_upload_status" || msgType == "version_info" {
			recordDebugNotice(msgType, data)
		}
		s.emit(msgType, data)
		return
	}
	if s.ws == nil {
		return
	}
	s.wsMutex.Lock()
	defer s.wsMutex.Unlock()
	if s.wsClosed {
		return
	}
	msg := map[string]interface{}{
		"type": msgType,
		"data": data,
	}
	if msgType == "error" || msgType == "github_upload_error" {
		recordProgramDebugError(msgType, data)
	} else if msgType == "log" || msgType == "github_upload_status" || msgType == "version_info" {
		recordDebugNotice(msgType, data)
	}
	s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := s.ws.WriteJSON(msg); err != nil {
		s.wsClosed = true
		recordProgramDebugError("websocket_write", err.Error())
		sendLog("WebSocket 发送失败: " + err.Error())
	}
}

func (s *appSession) isBackgroundTask() bool {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	return s.backgroundTask
}

func (s *appSession) startTask(run taskStarter) {
	s.startTaskNamed("任务", "", nil, run)
}

func (s *appSession) startTaskNamed(label string, mode string, params map[string]interface{}, run taskStarter) {
	if normalizeTaskNamespace(s.modeNamespace) == "nsb" {
		label = strings.ReplaceAll(label, "官方", "非标")
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := s.beginTask(cancel, label, mode, params)
	if !started {
		cancel()
		s.sendWSMessage("error", "已有任务正在运行，请等待完成后再试")
		return
	}
	safeGo("task", s, func() {
		defer cancel()
		defer func() {
			s.endTask()
			if ctx.Err() != nil {
				s.sendWSMessage("task_stopped", nil)
				return
			}
			s.sendWSMessage("task_complete", nil)
		}()
		run(ctx, s)
	})
}

func (s *appSession) runTaskSync(run taskStarter) error {
	ctx, cancel := context.WithCancel(context.Background())
	started := s.beginTask(cancel, "", "", nil)
	if !started {
		cancel()
		return context.Canceled
	}
	defer cancel()
	defer s.endTask()
	run(ctx, s)
	return nil
}

func (s *appSession) stopTask() {
	s.cancelTask(true)
}

func (s *appSession) cancelTaskSilently() {
	s.cancelTask(false)
}

func (s *appSession) enableBackgroundTask() bool {
	s.taskMutex.Lock()
	if !s.isTaskRunning {
		s.taskMutex.Unlock()
		return false
	}
	s.backgroundTask = true
	s.backgroundSnapshot.Running = true
	s.backgroundSnapshot.UpdatedAt = time.Now()
	s.taskMutex.Unlock()
	registerBackgroundTaskSession(s)
	return true
}

func (s *appSession) shouldCancelOnDisconnect() bool {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	return s.isTaskRunning && !s.backgroundTask
}

func (s *appSession) cancelTask(withLog bool) {
	s.taskMutex.Lock()
	cancel := s.taskCancel
	s.taskMutex.Unlock()
	if cancel != nil {
		cancel()
		if withLog {
			s.markBackgroundStopping()
			s.sendWSMessage("log", "已发送强制终止信号，正在清理当前任务...")
		}
	}
}

func (s *appSession) markBackgroundStopping() {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if !s.backgroundTask {
		return
	}
	s.backgroundSnapshot.Phase = "正在停止"
	s.backgroundSnapshot.Message = "已发送停止信号，正在整理当前可用结果"
	s.backgroundSnapshot.UpdatedAt = time.Now()
}

func (s *appSession) beginTask(cancel context.CancelFunc, label string, mode string, params map[string]interface{}) bool {
	globalTaskMutex.Lock()
	defer globalTaskMutex.Unlock()
	namespace := normalizeTaskNamespace(s.modeNamespace)
	clearCompletedBackgroundTaskSession(namespace)
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if activeTaskNamespaces[namespace] > 0 {
		return false
	}
	if s.isTaskRunning {
		return false
	}
	activeTaskNamespaces[namespace]++
	s.isTaskRunning = true
	s.taskCancel = cancel
	s.backgroundTask = false
	now := time.Now()
	s.backgroundSnapshot = backgroundTaskSnapshot{Label: label, Mode: mode, Phase: "准备中", Message: "任务准备中", Running: true, StartedAt: now, UpdatedAt: now, Params: params}
	atomic.AddInt32(&globalRunningTasks, 1)
	return true
}

func (s *appSession) endTask() {
	globalTaskMutex.Lock()
	defer globalTaskMutex.Unlock()
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.isTaskRunning {
		atomic.AddInt32(&globalRunningTasks, -1)
		namespace := normalizeTaskNamespace(s.modeNamespace)
		if activeTaskNamespaces[namespace] > 0 {
			activeTaskNamespaces[namespace]--
		}
	}
	s.isTaskRunning = false
	s.taskCancel = nil
	s.backgroundSnapshot.Running = false
	s.backgroundSnapshot.UpdatedAt = time.Now()
}

func registerBackgroundTaskSession(session *appSession) {
	backgroundTaskMutex.Lock()
	defer backgroundTaskMutex.Unlock()
	namespace := normalizeTaskNamespace(session.modeNamespace)
	backgroundTaskSessions[namespace] = session
}

func clearCompletedBackgroundTaskSession(namespace string) {
	backgroundTaskMutex.Lock()
	defer backgroundTaskMutex.Unlock()
	session := backgroundTaskSessions[normalizeTaskNamespace(namespace)]
	if session == nil {
		return
	}
	session.taskMutex.Lock()
	defer session.taskMutex.Unlock()
	if session.isTaskRunning {
		return
	}
	session.backgroundTask = false
	delete(backgroundTaskSessions, normalizeTaskNamespace(namespace))
}

func currentBackgroundTaskSession(namespace string) *appSession {
	backgroundTaskMutex.Lock()
	defer backgroundTaskMutex.Unlock()
	session := backgroundTaskSessions[normalizeTaskNamespace(namespace)]
	if session == nil {
		return nil
	}
	session.taskMutex.Lock()
	defer session.taskMutex.Unlock()
	if !session.backgroundTask {
		return nil
	}
	return session
}

func (s *appSession) backgroundSummary() backgroundTaskSnapshot {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	s.backgroundSnapshot.Running = s.isTaskRunning
	return s.backgroundSnapshot
}

func (s *appSession) attachWebSocket(conn *websocket.Conn) {
	s.wsMutex.Lock()
	defer s.wsMutex.Unlock()
	s.ws = conn
	s.wsClosed = false
}

func (s *appSession) updateBackgroundSnapshotFromMessage(msgType string, data interface{}) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if !s.isTaskRunning && !s.backgroundTask {
		return
	}
	now := time.Now()
	s.backgroundSnapshot.UpdatedAt = now
	s.backgroundSnapshot.Running = s.isTaskRunning
	s.applyBackgroundMessageLocked(msgType, data)
}

func (s *appSession) applyBackgroundMessageLocked(msgType string, data interface{}) {
	switch msgType {
	case "log":
		s.backgroundSnapshot.Message = fmt.Sprint(data)
	case "scan_progress", "test_progress":
		m, ok := data.(map[string]interface{})
		if !ok {
			return
		}
		phase := "扫描中"
		current := asInt(m["current"])
		total := asInt(m["total"])
		if msgType == "test_progress" {
			phase = "详细测试中"
		}
		s.backgroundSnapshot.ScanFailed = max(current-s.backgroundSnapshot.ScanSuccess, 0)
		s.updateProgressSnapshotLocked(phase, current, total, phase)
	case "official_speed_progress":
		m, ok := data.(map[string]interface{})
		if !ok {
			return
		}
		s.backgroundSnapshot.SpeedTotal = asInt(m["limit"])
		s.backgroundSnapshot.SpeedQualified = asInt(m["qualified"])
		s.updateProgressSnapshotLocked("测速中", asInt(m["qualified"]), asInt(m["limit"]), "测速中")
	case "scan_result":
		s.backgroundSnapshot.ResultCount++
		s.backgroundSnapshot.ScanSuccess++
		s.backgroundSnapshot.ScanTotal++
	case "speed_test_result":
		s.backgroundSnapshot.SpeedCount++
		s.updateBackgroundSpeedStatsLocked(data)
	case "test_result":
		s.backgroundSnapshot.TestCount++
		s.backgroundSnapshot.ScanSuccess++
		s.backgroundSnapshot.ScanTotal++
	case "scan_complete_wait_dc":
		if list, ok := data.([]DataCenterInfo); ok {
			s.backgroundSnapshot.DCCount = len(list)
			names := make([]string, 0, len(list))
			for _, dc := range list {
				name := dc.DataCenter
				if dc.City != "" {
					name += "(" + dc.City + ")"
				}
				names = append(names, name)
			}
			s.backgroundSnapshot.DCInfo = strings.Join(names, ", ")
		}
		s.backgroundSnapshot.Phase = "扫描完成"
		s.backgroundSnapshot.Message = "扫描完成，等待详细测试或后续阶段"
	case "test_complete":
		s.backgroundSnapshot.Phase = "详细测试完成"
		s.backgroundSnapshot.Message = "详细测试完成"
	case "official_speed_complete":
		s.backgroundSnapshot.Phase = "测速完成"
		s.backgroundSnapshot.Message = "测速完成"
		s.finishBackgroundStatsLocked()
	case "task_stopped":
		s.backgroundSnapshot.Phase = "已停止"
		s.backgroundSnapshot.Message = "任务已停止"
		s.backgroundSnapshot.Running = false
	case "task_complete":
		s.backgroundSnapshot.Phase = "完成"
		s.backgroundSnapshot.Message = "任务完成"
		s.backgroundSnapshot.Running = false
	}
}

func (s *appSession) updateBackgroundSpeedStatsLocked(data interface{}) {
	s.backgroundSnapshot.SpeedSuccess++
	s.backgroundSnapshot.SpeedTotal++
	speedText := ""
	switch value := data.(type) {
	case map[string]string:
		speedText = value["speed"]
	case map[string]interface{}:
		speedText = fmt.Sprint(value["speed"])
	}
	if speedMB, ok := parseSpeedMBForSort(speedText); ok {
		if speedMB >= s.backgroundSpeedMinLocked() {
			s.backgroundSnapshot.SpeedQualified++
		}
		return
	}
	s.backgroundSnapshot.SpeedSuccess--
	s.backgroundSnapshot.SpeedFailed++
}

func (s *appSession) backgroundSpeedMinLocked() float64 {
	value, ok := s.backgroundSnapshot.Params["speedMin"]
	if !ok {
		return 0
	}
	switch n := value.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		parsed, ok := parseSpeedMBForSort(n + "MB/s")
		if ok {
			return parsed
		}
	}
	return 0
}

func (s *appSession) finishBackgroundStatsLocked() {
}

func (s *appSession) updateProgressSnapshotLocked(phase string, current int, total int, message string) {
	s.backgroundSnapshot.Phase = phase
	s.backgroundSnapshot.Message = message
	s.backgroundSnapshot.Current = current
	s.backgroundSnapshot.Total = total
	if total > 0 {
		s.backgroundSnapshot.Percent = float64(current) / float64(total) * 100
	}
}
