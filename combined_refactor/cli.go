package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type cliConfig struct {
	enabled         bool
	configResolved  bool
	mode            string
	scanMode        string
	ipType          int
	threads         int
	offThreads      int
	port            int
	delay           int
	offDelay        int
	dc              string
	offURL          string
	outFile         string
	speedLimit      int
	speedMin        float64
	showProgress    bool
	noColor         bool
	compactIPv4     bool
	outIPType       string
	outQualified    string
	startRow        int
	endRow          int
	export          cliExportConfig
}

type cliExportConfig struct {
	ConfigFile    string `json:"-"`
	Format        string `json:"outformat"`
	Fields        string `json:"outfields"`
	Custom        string `json:"outcustom"`
	V6Bracket     bool   `json:"outv6bracket"`
	V6BracketSet  bool   `json:"-"`
	Separator     string `json:"outseparator"`
	GitHub        bool   `json:"github"`
	GitHubSet     bool   `json:"-"`
	GHRepo        string `json:"ghrepo"`
	GHBranch      string `json:"ghbranch"`
	GHPath        string `json:"ghpath"`
	GHMessage     string `json:"ghmessage"`
	GHToken       string `json:"ghtoken"`
	GHTokenFile   string `json:"ghtokenfile"`
	GHUpload      string `json:"ghupload"`
	EdgeTunnel    bool   `json:"edgetunnel"`
	EdgeTunnelSet bool   `json:"-"`
	ETHost        string `json:"ethost"`
	ETPassword    string `json:"etpassword"`
	ETMode        string `json:"etmode"`
}

type cliFileConfig struct {
	CLI             bool    `json:"cli"`
	Mode            string  `json:"mode"`
	ScanMode        string  `json:"scanmode"`
	IPType          int     `json:"offiptype"`
	Threads         int     `json:"offthreads"`
	Out             string  `json:"out"`
	Progress        bool    `json:"progress"`
	NoColor         bool    `json:"nocolor"`
	SkipGeo         bool    `json:"skipgeo"`
	URL             string  `json:"offurl"`
	DNS             string  `json:"dns"`
	Debug           any     `json:"debug"`
	CompactIPv4     bool    `json:"compactipv4"`
	TestPort        int     `json:"offport"`
	Delay           int     `json:"offdelay"`
	DC              string  `json:"offdc"`
	SpeedLimit      int     `json:"offspeedlimit"`
	SpeedMin        float64 `json:"offspeedmin"`
	OutIPType       string  `json:"outiptype"`
	OutQualified    string  `json:"outqualified"`
	Format          string  `json:"outformat"`
	Fields          string  `json:"outfields"`
	Custom          string  `json:"outcustom"`
	V6Bracket       bool    `json:"outv6bracket"`
	Separator       string  `json:"outseparator"`
	OutStartRow     int     `json:"outstartrow"`
	OutEndRow       int     `json:"outendrow"`
	GitHub          bool    `json:"github"`
	GHRepo          string  `json:"ghrepo"`
	GHBranch        string  `json:"ghbranch"`
	GHPath          string  `json:"ghpath"`
	GHMessage       string  `json:"ghmessage"`
	GHToken         string  `json:"ghtoken"`
	GHTokenFile     string  `json:"ghtokenfile"`
	GHUpload        string  `json:"ghupload"`
	EdgeTunnel      bool    `json:"edgetunnel"`
	ETHost          string  `json:"ethost"`
	ETPassword      string  `json:"etpassword"`
	ETMode          string  `json:"etmode"`
}

type cliResultRow map[string]string

type cliResultField struct {
	Key   string
	Label string
}

type cliCustomField struct {
	Key   string
	Label string
	Value string
}

var cliResultFields = []cliResultField{
	{Key: "ipport", Label: "ip:port"},
	{Key: "ip", Label: "IP地址"},
	{Key: "port", Label: "端口号"},
	{Key: "tls", Label: "TLS"},
	{Key: "lossRate", Label: "丢包率"},
	{Key: "latency", Label: "网络延迟"},
	{Key: "scanMode", Label: "扫描方式"},
	{Key: "speed", Label: "下载速度"},
	{Key: "outboundIP", Label: "出站IP"},
	{Key: "ipType", Label: "IP类型"},
	{Key: "originalInput", Label: "原始输入"},
	{Key: "dc", Label: "数据中心"},
	{Key: "dcCountry", Label: "落地区域"},
	{Key: "loc", Label: "源IP位置"},
	{Key: "region", Label: "地区"},
	{Key: "city", Label: "城市"},
	{Key: "asnNumber", Label: "ASN号码"},
	{Key: "asnOrg", Label: "ASN组织"},
	{Key: "visitScheme", Label: "访问协议"},
	{Key: "tlsVersion", Label: "TLS版本"},
	{Key: "sni", Label: "SNI"},
	{Key: "httpVersion", Label: "HTTP版本"},
	{Key: "warp", Label: "WARP"},
	{Key: "gateway", Label: "Gateway"},
	{Key: "rbi", Label: "RBI"},
	{Key: "kex", Label: "密钥交换"},
	{Key: "timestamp", Label: "时间戳"},
}

type cliFlagInfo struct {
	name         string
	description  string
	defaultValue string
}

var (
	ansiReset       = "\033[0m"
	ansiBold        = "\033[1m"
	ansiGreen       = "\033[32m"
	ansiBrightGreen = "\033[92m"
	ansiYellow      = "\033[33m"
	ansiRed         = "\033[31m"
	ansiCyan        = "\033[36m"
	ansiMagenta     = "\033[35m"

	cliCommonFlags = []cliFlagInfo{
		{name: "cli", description: "启用命令行模式：不带时启动 Web；-cli 进入交互菜单；-cli qs 按配置快速启动（请用 -cli 或 -cli=true）", defaultValue: "false"},
		{name: "port", description: "Web 服务监听端口", defaultValue: "13335"},
		{name: "user", description: "Web 认证用户名（不设置则不启用认证）", defaultValue: ""},
		{name: "password", description: "Web 认证密码（需同时设置 -user）", defaultValue: ""},
		{name: "session", description: "Web 登录会话有效期（分钟）", defaultValue: "720"},
		{name: "host", description: "服务监听地址；留空监听全部地址，Android APK 建议使用 127.0.0.1", defaultValue: ""},
		{name: "mode", description: "运行模式：official", defaultValue: "official"},
		{name: "scanmode", description: "扫描方式：tcping（默认，TCP 握手延迟）或 httping（HTTP TTFB，延迟比 tcping 高属正常，不同模式数据不可对比）", defaultValue: "tcping"},
		{name: "out", description: "输出文件名", defaultValue: "cfdata-results"},
		{name: "progress", description: "是否输出进度日志", defaultValue: "true"},
		{name: "nocolor", description: "禁用颜色输出（cmd 等不支持 ANSI 的终端可开启避免乱码）", defaultValue: "false"},
		{name: "skipgeo", description: "跳过地区/代理环境验证，CLI 启动测试前不再确认代理警告", defaultValue: "false"},
		{name: "url", description: "测速下载地址；auto 表示由后端自动选择内置测速源", defaultValue: autoSpeedURLValue},
		{name: "dns", description: "自定义 DNS 服务器，例如 1.1.1.1 或 223.5.5.5,8.8.8.8；默认系统 DNS 优先，失败回退内置 DNS；显式设置时强制使用指定 DNS", defaultValue: defaultDNSServers},
		{name: "debug", description: "调试输出等级：error、all；true 等同 error", defaultValue: "false"},
		{name: "compactipv4", description: "精简本地 IPv4 地址库：按 /24 子网测 TCP:80 连通性并覆盖 ips-v4.txt", defaultValue: "false"},
		{name: "config", description: "CLI 配置文件路径，不存在时在二进制目录自动生成模板", defaultValue: "二进制目录/cfdata-config.json"},
		{name: "outformat", description: "CLI 导出格式：csv 或 txt", defaultValue: "txt"},
		{name: "outfields", description: "CLI 导出字段：compact、all、ipport 或逗号分隔字段 key；可用 -outcustom 增加常量字段", defaultValue: "compact"},
		{name: "outcustom", description: "CLI 自定义导出字段，格式 标题:内容，多项用逗号分隔；在 -outfields 中可用标题排序，未写入则默认追加到最后", defaultValue: ""},
		{name: "outv6bracket", description: "TXT 导出时对 IPv6 地址加方括号（[IPv6]:端口）", defaultValue: "true"},
		{name: "outseparator", description: "TXT 导出字段分隔符；写 空格 表示空格", defaultValue: "-"},
		{name: "outqualified", description: "导出/上传合格结果筛选：all 全部 / qualified 仅合格 / unqualified 仅不合格", defaultValue: "all"},
		{name: "outiptype", description: "导出/上传 IP 类型筛选：all、ipv4 或 ipv6", defaultValue: "all"},
		{name: "outstartrow", description: "导出/上传开始行（筛选后第 1 行起计），只影响导出和上传内容", defaultValue: "1"},
		{name: "outendrow", description: "导出/上传结束行；0 表示至末尾，只影响导出和上传内容", defaultValue: "20"},
		{name: "github", description: "CLI 导出后上传到 GitHub", defaultValue: "false"},
		{name: "ghrepo", description: "GitHub 仓库，格式 owner/repo", defaultValue: ""},
		{name: "ghbranch", description: "GitHub 分支", defaultValue: "main"},
		{name: "ghpath", description: "GitHub 目标路径；留空时按 -outformat 自动使用 results/ip.csv 或 results/ip.txt", defaultValue: "<自动>"},
		{name: "ghmessage", description: "GitHub 提交信息", defaultValue: "update cfdata results"},
		{name: "ghtoken", description: "GitHub token（不推荐直接写入配置；强烈建议使用仅限制指定仓库读写权限的 token，并确保仓库内无重要数据）", defaultValue: ""},
		{name: "ghtokenfile", description: "GitHub token 文件路径（强烈建议文件内 token 仅限制指定仓库读写权限，并确保仓库内无重要数据）", defaultValue: ""},
		{name: "ghupload", description: "快速上传指定文件到 GitHub，不执行测试；需配合 -github", defaultValue: ""},
		{name: "edgetunnel", description: "CLI 导出后上传到 edgetunnel", defaultValue: "false"},
		{name: "ethost", description: "edgetunnel 主机地址，例如 https://example.com", defaultValue: ""},
		{name: "etpassword", description: "edgetunnel 密码", defaultValue: ""},
		{name: "etmode", description: "edgetunnel 上传模式：overwrite 覆盖 / append 追加", defaultValue: "overwrite"},
	}
	cliOfficialFlags = []cliFlagInfo{
		{name: "offiptype", description: "官方模式 IP 类型：4 或 6", defaultValue: "4"},
		{name: "offthreads", description: "官方模式扫描并发数", defaultValue: "100"},
		{name: "offport", description: "官方模式详细测试与测速端口", defaultValue: "443"},
		{name: "offdelay", description: "官方模式延迟阈值（毫秒）", defaultValue: "500"},
		{name: "offdc", description: "指定数据中心；不填时自动选择最低延迟数据中心", defaultValue: ""},
		{name: "offspeedlimit", description: "官方模式测速达标结果上限；0 表示关闭官方测速", defaultValue: "5"},
		{name: "offspeedmin", description: "官方模式测速达标下限，单位 MB/s", defaultValue: "0.1"},
		{name: "offurl", description: "官方模式测速下载地址；auto 表示由后端自动选择内置测速源", defaultValue: autoSpeedURLValue},
	}
 func() {
		fmt.Printf("%s[official]%s %s\n", ansiYellow, ansiReset, speedRateLimitMessage)
	})
	return updated
}

func printCLIConfig(cfg *cliConfig) {
	fmt.Printf("%s %s\n", colorize("CFData-WEB 版本:", ansiBold+ansiGreen), appVersion)
	fmt.Printf("%s %s\n", colorize("调试等级:", ansiBold+ansiGreen), debugFlagValue{}.String())
	checkAndPrintUpdate("")
	line := func(label, value string) { fmt.Printf("  %s：%s\n", label, value) }
	boolLabel := func(v bool) string { if v { return "开启" }; return "关闭" }
	scanLabel := "TCPing"
	if strings.EqualFold(strings.TrimSpace(cfg.scanMode), "httping") { scanLabel = "HTTPing" }
	speedURLLabel := speedTestURL
	if isAutoSpeedURL(speedTestURL) { speedURLLabel = "自动选择" }
	dnsLabel := "系统默认"
	if customDNSForced { dnsLabel = customDNSServer } else if strings.TrimSpace(customDNSServer) != "" && customDNSServer != defaultDNSServers { dnsLabel = "系统默认（回退 " + customDNSServer + "）" }
	ipVersionLabel := "IPv4"
	if cfg.ipType == 6 { ipVersionLabel = "IPv6" }
	fmt.Println(colorize("----------------------------------------", ansiCyan))
	fmt.Println(colorize("当前配置", ansiBold+ansiCyan))
	line("模式", "官方优选")
	line("扫描方式", scanLabel)
	line("IP类型", ipVersionLabel)
	line("输出文件名", cfg.outFile)
	line("文件格式", cfg.export.Format)
	line("开始行", strconv.Itoa(cfg.startRow))
	if cfg.endRow > 0 { line("结束行", strconv.Itoa(cfg.endRow)); line("输出总量", fmt.Sprintf("%d行", cfg.endRow-cfg.startRow+1)) } else { line("结束行", "不限"); line("输出总量", "不限") }
	line("并发数量", strconv.Itoa(cfg.threads))
	line("扫描合格延迟", fmt.Sprintf("%dms", cfg.delay))
	line("DNS", dnsLabel)
	line("测试端口", strconv.Itoa(cfg.port))
	dcLabel := strings.TrimSpace(cfg.dc); if dcLabel == "" { dcLabel = "自动" }
	line("数据中心", dcLabel)
	line("测速网址", speedURLLabel)
	line("自动测速", boolLabel(cfg.speedLimit > 0))
	line("测速结果数量", strconv.Itoa(cfg.speedLimit))
	line("测试合格速度", fmt.Sprintf("%.2fMB/s", cfg.speedMin))
	fmt.Println(colorize("----------------------------------------", ansiCyan))
}

func maskSecret(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if len(value) <= 8 {
		return "********"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func printCLIUsage() {
	printBanner()
	fmt.Fprintf(flag.CommandLine.Output(), "%s\n", colorize("CFData 命令行帮助", ansiBold+ansiGreen))
	fmt.Fprintf(flag.CommandLine.Output(), "版本: %s\n", appVersion)
	checkAndPrintUpdate("")
	fmt.Fprintf(flag.CommandLine.Output(), "\n")
	fmt.Fprintf(flag.CommandLine.Output(), "默认行为: 不带 -cli 时启动 Web 服务；-cli 进入交互菜单；-cli qs 按配置快速启动\n")
	fmt.Fprintf(flag.CommandLine.Output(), "CLI 用法: ./cfdata-test -cli（菜单）或 -cli qs（快速启动）或 -cli -mode=official ...（直接执行）\n")
	fmt.Fprintf(flag.CommandLine.Output(), "\n")
	printCLIUsageGroup("通用参数", cliCommonFlags)
	printCLIUsageGroup("官方模式参数", cliOfficialFlags)
}

func printCLIUsageGroup(title string, rows []cliFlagInfo) {
	fmt.Fprintf(flag.CommandLine.Output(), "%s\n", colorize("----------------------------------------", ansiCyan))
	fmt.Fprintf(flag.CommandLine.Output(), "%s\n", colorize(title, ansiBold+ansiCyan))
	for _, row := range rows {
		fmt.Fprintf(flag.CommandLine.Output(), "%s-%s%s\n", ansiBold, row.name, ansiReset)
		fmt.Fprintf(flag.CommandLine.Output(), "  %s %s\n", colorize("说明:", ansiYellow), row.description)
		fmt.Fprintf(flag.CommandLine.Output(), "  %s %s\n", colorize("默认:", ansiYellow), colorizeCLIDefaultValue(row.defaultValue))
	}
}

func lookupCLIFlagDescription(rows []cliFlagInfo, name string) string {
	for _, row := range rows {
		if row.name == name {
			return row.description
		}
	}
	return ""
}

func colorize(text string, code string) string {
	if text == "" {
		return text
	}
	return code + text + ansiReset
}

func colorizeLatencyString(latency string) string {
	ms, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(latency), " ms"))
	if err != nil {
		return latency
	}
	return colorizeLatencyMS(ms)
}

func colorizeLatencyMS(ms int) string {
	text := fmt.Sprintf("%dms", ms)
	if ms <= 50 {
		return colorize(text, ansiGreen)
	}
	if ms <= 100 {
		return colorize(text, ansiBrightGreen)
	}
	if ms <= 200 {
		return colorize(text, ansiYellow)
	}
	if ms <= 250 {
		return colorize(text, ansiYellow)
	}
	if ms <= 3000 {
		return colorize(text, ansiYellow)
	}
	return colorize(text, ansiRed)
}

func colorizeLossRate(lossRate float64) string {
	text := fmt.Sprintf("%.0f%%", lossRate*100)
	if lossRate <= 0 {
		return colorize(text, ansiGreen)
	}
	if lossRate < 0.5 {
		return colorize(text, ansiYellow)
	}
	return colorize(text, ansiRed)
}

func colorizeSpeedString(speed string) string {
	if strings.Contains(speed, "MB/s") {
		value, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(speed, "MB/s")), 64)
		if err == nil {
			if value > 10 {
				return colorize(speed, ansiGreen)
			}
			return colorize(speed, ansiYellow)
		}
	}
	if strings.Contains(strings.ToLower(speed), "错误") || strings.Contains(speed, "失败") || strings.Contains(speed, "0MB/s") {
		return colorize(speed, ansiRed)
	}
	return speed
}

func colorizeCLIParamValue(value string, defaultValue string) string {
	if value == defaultValue {
		if value == "" {
			return colorize("<空>", ansiGreen)
		}
		return colorize(value, ansiGreen)
	}
	if value == "" {
		return colorize("<空>", ansiYellow)
	}
	if value == "true" {
		return colorize(value, ansiGreen)
	}
	if value == "false" {
		return colorize(value, ansiRed)
	}
	return colorize(value, ansiMagenta)
}

func colorizeCLIDefaultValue(value string) string {
	if value == "" {
		return colorize("<空>", ansiYellow)
	}
	return colorize(value, ansiGreen)
}

func setCLIProgress(session *appSession, phase string, current int, total int) {
	session.progressMutex.Lock()
	defer session.progressMutex.Unlock()
	if session.progressState == nil {
		session.progressState = map[string][2]int{}
	}
	state := session.progressState[phase]
	if total <= 0 {
		total = state[1]
	}
	if current < state[0] {
		current = state[0]
	}
	session.progressState[phase] = [2]int{current, total}
}

func maybePrintCLIProgress(session *appSession, phase string, current, total int) {
	if total <= 0 {
		return
	}
	session.progressMutex.Lock()
	if session.progressPrintTime == nil {
		session.progressPrintTime = map[string]time.Time{}
	}
	if session.progressPrintPercent == nil {
		session.progressPrintPercent = map[string]float64{}
	}
	now := time.Now()
	percent := float64(current) / float64(total) * 100
	lastTime := session.progressPrintTime[phase]
	lastPercent := session.progressPrintPercent[phase]

	shouldPrint := false
	switch {
	case lastTime.IsZero():
		shouldPrint = true
	case current >= total:
		shouldPrint = true
	case percent-lastPercent >= 5.0:
		shouldPrint = true
	case now.Sub(lastTime) >= 3*time.Second:
		shouldPrint = true
	}

	if shouldPrint {
		session.progressPrintTime[phase] = now
		session.progressPrintPercent[phase] = percent
	}
	session.progressMutex.Unlock()

	if !shouldPrint {
		return
	}
	fmt.Printf("%s[%s-progress]%s %s\n", ansiCyan, phase, ansiReset, colorize(fmt.Sprintf("[%d/%d %.2f%%]", current, total, percent), ansiCyan))
}

func renderCLIProgress(session *appSession, phase string) string {
	session.progressMutex.Lock()
	defer session.progressMutex.Unlock()
	if session.progressState == nil {
		return colorize("[0/0]", ansiCyan)
	}
	state, ok := session.progressState[phase]
	if !ok {
		return colorize("[0/0]", ansiCyan)
	}
	if state[1] <= 0 {
		return colorize(fmt.Sprintf("[%d/0]", state[0]), ansiCyan)
	}
	percent := float64(state[0]) / float64(state[1]) * 100
	return colorize(fmt.Sprintf("[%d/%d %.2f%%]", state[0], state[1], percent), ansiCyan)
}

func advanceCLIProgress(session *appSession, phase string) string {
	session.progressMutex.Lock()
	defer session.progressMutex.Unlock()
	if session.progressState == nil {
		session.progressState = map[string][2]int{}
	}
	state := session.progressState[phase]
	if state[1] > 0 && state[0] < state[1] {
		state[0]++
		session.progressState[phase] = state
	}
	if state[1] <= 0 {
		return colorize(fmt.Sprintf("[%d/0]", state[0]), ansiCyan)
	}
	percent := float64(state[0]) / float64(state[1]) * 100
	return colorize(fmt.Sprintf("[%d/%d %.2f%%]", state[0], state[1], percent), ansiCyan)
}

func asInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		value, err := strconv.Atoi(n)
		if err == nil {
			return value
		}
	}
	return 0
}

func writeCLIExportAndMaybeUpload(cfg *cliConfig, rows []cliResultRow, mode string) error {
	if len(rows) == 0 {
		return nil
	}
	content, err := formatCLIResults(rows, cfg.export)
	if err != nil {
		return err
	}
	filename := cfg.outFile
	if strings.TrimSpace(filename) == "" {
		filename = "ip." + cfg.export.Format
	}
	if ext := "." + cfg.export.Format; !strings.HasSuffix(strings.ToLower(filename), ext) {
		filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ext
	}
	filename = safeFilename(filename)
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := writeUTF8BOM(file); err != nil {
		os.Remove(filename)
		return err
	}

	if _, err := io.WriteString(file, content); err != nil {
		return err
	}
	fmt.Printf("[%s-output] %s (%d rows, %s)\n", mode, filename, len(rows), cfg.export.Format)
	if cfg.export.GitHub {
		if err := uploadCLIExportToGitHub(cfg, content); err != nil {
			return err
		}
	}
	if cfg.export.EdgeTunnel {
		if err := uploadCLIExportToEdgetunnel(cfg, content); err != nil {
			return err
		}
	}
	return nil
}

func resolveCLITextSeparator(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	switch strings.ToLower(value) {
	case "空格", "space":
		return " "
	case "\\t", "tab":
		return "\t"
	}
	return value
}

func formatCLIResults(rows []cliResultRow, cfg cliExportConfig) (string, error) {
	customFields := parseCLICustomFields(cfg.Custom)
	fields := resolveCLIFields(cfg.Fields, cfg.Format, rows, customFields)
	rows = applyCLICustomFields(rows, customFields)
	if cfg.Format == "txt" {
		separator := resolveCLITextSeparator(cfg.Separator)
		var b strings.Builder
		for _, row := range rows {
			ipport := row["ipport"]
			if ip := strings.TrimSpace(row["ip"]); ip != "" {
				if cfg.V6Bracket && strings.Contains(ip, ":") {
					ipport = "[" + ip + "]:" + row["port"]
				} else {
					ipport = ip + ":" + row["port"]
				}
			} else if ipport == "" {
				ipport = row["ip"] + ":" + row["port"]
			}
			extras := make([]string, 0, len(fields))
			for _, field := range fields {
				if field == "ipport" {
					continue
				}
				if value := strings.TrimSpace(row[field]); value != "" {
					extras = append(extras, value)
				}
			}
			b.WriteString(ipport)
			if len(extras) > 0 {
				b.WriteString("#")
				b.WriteString(strings.Join(extras, separator))
			}
			b.WriteString("\n")
		}
		return b.String(), nil
	}

	var b strings.Builder
	writer := csv.NewWriter(&b)
	headers := make([]string, 0, len(fields))
	for _, field := range fields {
		headers = append(headers, cliFieldLabel(field, customFields))
	}
	if err := writer.Write(headers); err != nil {
		return "", err
	}
	for _, row := range rows {
		values := make([]string, 0, len(fields))
		for _, field := range fields {
			values = append(values, row[field])
		}
		if err := writer.Write(values); err != nil {
			return "", err
		}
	}
	writer.Flush()
	return b.String(), writer.Error()
}

func resolveCLIFields(spec, format string, rows []cliResultRow, customFields []cliCustomField) []string {
	spec = strings.TrimSpace(strings.ToLower(spec))
	customByKey := map[string]bool{}
	for _, field := range customFields {
		customByKey[field.Key] = true
	}
	appendCustomFields := func(fields []string) []string {
		seen := map[string]bool{}
		result := make([]string, 0, len(fields)+len(customFields))
		for _, field := range fields {
			if field == "" || seen[field] {
				continue
			}
			result = append(result, field)
			seen[field] = true
		}
		for _, field := range customFields {
			if !seen[field.Key] {
				result = append(result, field.Key)
			}
		}
		return result
	}
	if spec == "" || spec == "compact" {
		if format == "txt" {
			return appendCustomFields([]string{"ipport", "dc", "loc"})
		}
		if rowsAreOfficial(rows) {
			if rowsHaveField(rows, "speed") {
				return appendCustomFields([]string{"ip", "port", "latency", "speed", "dc", "region", "city"})
			}
			return appendCustomFields([]string{"ip", "port", "latency", "dc", "region", "city"})
		}
		return appendCustomFields([]string{"ip", "port", "tls", "latency", "speed", "outboundIP", "ipType", "originalInput", "dc", "loc", "region", "city", "asnNumber", "asnOrg"})
	}
	if spec == "ipport" {
		return appendCustomFields([]string{"ipport"})
	}
	if spec == "all" {
		fields := make([]string, 0, len(cliResultFields))
		for _, field := range cliResultFields {
			if field.Key == "ipport" {
				continue
			}
			for _, row := range rows {
				if strings.TrimSpace(row[field.Key]) != "" {
					fields = append(fields, field.Key)
					break
				}
			}
		}
		return appendCustomFields(fields)
	}
	parts := strings.Split(spec, ",")
	fields := make([]string, 0, len(parts))
	valid := map[string]string{"ipport": "ipport"}
	for _, field := range cliResultFields {
		valid[strings.ToLower(field.Key)] = field.Key
	}
	for _, part := range parts {
		field := strings.TrimSpace(part)
		fieldLower := strings.ToLower(field)
		if field != "" && (valid[fieldLower] != "" || customByKey[fieldLower]) {
			if customByKey[fieldLower] {
				field = fieldLower
			} else {
				field = valid[fieldLower]
			}
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return appendCustomFields([]string{"ipport"})
	}
	return appendCustomFields(fields)
}

func rowsAreOfficial(rows []cliResultRow) bool {
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if hasAnyCLIField(row, "tls", "outboundIP", "ipType", "loc", "asnNumber", "asnOrg", "visitScheme", "tlsVersion", "sni", "httpVersion", "warp", "gateway", "rbi", "kex", "timestamp") {
			return false
		}
	}
	return true
}

func rowsHaveField(rows []cliResultRow, field string) bool {
	for _, row := range rows {
		if strings.TrimSpace(row[field]) != "" {
			return true
		}
	}
	return false
}

func hasAnyCLIField(row cliResultRow, fields ...string) bool {
	for _, field := range fields {
		if strings.TrimSpace(row[field]) != "" {
			return true
		}
	}
	return false
}

func cliFieldLabel(key string, customFields []cliCustomField) string {
	for _, field := range customFields {
		if field.Key == key {
			return field.Label
		}
	}
	for _, field := range cliResultFields {
		if field.Key == key {
			return field.Label
		}
	}
	return key
}

func parseCLICustomFields(spec string) []cliCustomField {
	parts := strings.Split(spec, ",")
	fields := make([]cliCustomField, 0, len(parts))
	seen := map[string]int{}
	for _, part := range parts {
		item := strings.TrimSpace(part)
		key := ""
		if keyValue := strings.SplitN(item, "=", 2); len(keyValue) == 2 {
			key = strings.ToLower(strings.TrimSpace(keyValue[0]))
			item = strings.TrimSpace(keyValue[1])
		}
		labelValue := strings.SplitN(item, ":", 2)
		if len(labelValue) != 2 || strings.TrimSpace(labelValue[0]) == "" || strings.TrimSpace(labelValue[1]) == "" {
			continue
		}
		label := strings.TrimSpace(labelValue[0])
		if label == "" {
			label = key
		}
		if key == "" {
			key = strings.ToLower(label)
		}
		baseKey := key
		if count := seen[baseKey]; count > 0 {
			for {
				key = fmt.Sprintf("%s%d", baseKey, count)
				if seen[key] == 0 {
					break
				}
				count++
			}
		}
		fields = append(fields, cliCustomField{Key: key, Label: label, Value: strings.TrimSpace(labelValue[1])})
		seen[baseKey]++
		if key != baseKey {
			seen[key]++
		}
	}
	return fields
}

func applyCLICustomFields(rows []cliResultRow, fields []cliCustomField) []cliResultRow {
	if len(fields) == 0 {
		return rows
	}
	for _, row := range rows {
		for _, field := range fields {
			row[field.Key] = field.Value
		}
	}
	return rows
}

func officialScanRows(scanResults []ScanResult, scanMode string) []cliResultRow {
	rows := make([]cliResultRow, 0, len(scanResults))
	modeLabel := scanModeLabel(scanMode)
	for _, res := range scanResults {
		rows = append(rows, cliResultRow{"ip": res.IP, "port": strconv.Itoa(res.Port), "ipport": fmt.Sprintf("%s:%d", res.IP, res.Port), "dc": res.DataCenter, "dcCountry": res.DCCountry, "region": res.Region, "city": res.City, "latency": res.LatencyStr, "scanMode": modeLabel})
	}
	return rows
}

func officialResultRows(scanResults []ScanResult, testResults []TestResult, scanMode string) []cliResultRow {
	if len(testResults) == 0 {
		rows := officialScanRows(scanResults, scanMode)
		sortOfficialRows(rows)
		return rows
	}
	scanByIP := make(map[string]ScanResult, len(scanResults))
	for _, res := range scanResults {
		scanByIP[res.IP] = res
	}
	modeLabel := scanModeLabel(scanMode)
	rows := make([]cliResultRow, 0, len(testResults))
	for _, res := range testResults {
		scan := scanByIP[res.IP]
		port := scan.Port
		if port == 0 {
			port = res.Port
		}
		rows = append(rows, cliResultRow{"ip": res.IP, "port": strconv.Itoa(port), "ipport": fmt.Sprintf("%s:%d", res.IP, port), "dc": scan.DataCenter, "dcCountry": scan.DCCountry, "region": scan.Region, "city": scan.City, "latency": fmt.Sprintf("%dms", res.AvgLatency/time.Millisecond), "speed": res.Speed, "scanMode": modeLabel})
		if rows[len(rows)-1]["dc"] == "" {
			rows[len(rows)-1]["dc"] = res.DataCenter
			rows[len(rows)-1]["dcCountry"] = res.DCCountry
			rows[len(rows)-1]["region"] = res.Region
			rows[len(rows)-1]["city"] = res.City
		}
	}
	sortOfficialRows(rows)
	return rows
}

func sortOfficialRows(rows []cliResultRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		speedI, okI := parseSpeedMBForSort(rows[i]["speed"])
		speedJ, okJ := parseSpeedMBForSort(rows[j]["speed"])
		if okI != okJ {
			return okI
		}
		if okI && speedI != speedJ {
			return speedI > speedJ
		}
		latencyI := parseLatencyMSForSort(rows[i]["latency"])
		latencyJ := parseLatencyMSForSort(rows[j]["latency"])
		if latencyI != latencyJ {
			return latencyI < latencyJ
		}
		return rows[i]["ip"] < rows[j]["ip"]
	})
}

func parseSpeedMBForSort(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "MB/s") {
		return 0, false
	}
	speed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "MB/s")), 64)
	if err != nil {
		return 0, false
	}
	return speed, true
}

func parseLatencyMSForSort(value string) float64 {
	latency, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "ms")), 64)
	if err != nil {
		return math.MaxFloat64
	}
	return latency
}

func filterCLIResultRowsByIPType(rows []cliResultRow, filter string) []cliResultRow {
	filter = normalizeIPTypeFilter(filter)
	if filter == "all" {
		return rows
	}
	filtered := make([]cliResultRow, 0, len(rows))
	for _, row := range rows {
		if strings.EqualFold(cliRowIPType(row), filter) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func cliRowIPType(row cliResultRow) string {
	if value := strings.TrimSpace(row["ipType"]); value != "" {
		return strings.ToLower(value)
	}
	if strings.Contains(row["ip"], ":") {
		return "ipv6"
	}
	return "ipv4"
}

func filterCLIResultRowsByQualified(rows []cliResultRow, filter string, speedMin float64) []cliResultRow {
	filter, ok := normalizeCLIQualifiedFilter(filter)
	if !ok || filter == "all" {
		return rows
	}
	filtered := make([]cliResultRow, 0, len(rows))
	for _, row := range rows {
		if cliSpeedStatus(row["speed"], speedMin) == filter {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func normalizeCLIQualifiedFilter(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "all", true
	case "qualified", "合格":
		return "qualified", true
	case "unqualified", "不合格":
		return "unqualified", true
	}
	return "", false
}

func sliceCLIResultRowRange(rows []cliResultRow, start, end int) []cliResultRow {
	if start < 1 {
		start = 1
	}
	if start > len(rows) {
		return nil
	}
	if end <= 0 || end > len(rows) {
		end = len(rows)
	}
	if end < start {
		return nil
	}
	return rows[start-1 : end]
}

func applyCLIExportFilters(cfg *cliConfig, rows []cliResultRow, speedMin float64) []cliResultRow {
	rows = filterCLIResultRowsByIPType(rows, cfg.outIPType)
	rows = filterCLIResultRowsByQualified(rows, cfg.outQualified, speedMin)
	return sliceCLIResultRowRange(rows, cfg.startRow, cfg.endRow)
}

func normalizeIPTypeFilter(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all", "全部", "全部展示":
		return "all"
	case "4", "ipv4":
		return "ipv4"
	case "6", "ipv6":
		return "ipv6"
	default:
		return ""
	}
}

func cliFieldKeyFromHeader(header string) string {
	header = strings.TrimSpace(header)
	for _, field := range cliResultFields {
		if field.Label == header {
			return field.Key
		}
	}
	return ""
}

func uploadCLIExportToGitHub(cfg *cliConfig, content string) error {
	parts := strings.Split(strings.TrimSpace(cfg.export.GHRepo), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("-ghrepo 必须是 owner/repo")
	}
	if strings.TrimSpace(cfg.export.GHToken) == "" {
		return fmt.Errorf("启用 -github 时需要 -ghtoken 或 -ghtokenfile")
	}
	params := githubUploadRequest{Token: cfg.export.GHToken, Owner: parts[0], Repo: parts[1], Branch: cfg.export.GHBranch, Path: cfg.export.GHPath, Message: cfg.export.GHMessage, Content: content}
	downloadURL, err := uploadGitHubContentWithRetry(context.Background(), params, func(attempt, total int, err error) {
		if err == nil {
			fmt.Printf("%s[github]%s upload attempt %d/%d\n", ansiYellow, ansiReset, attempt, total)
			return
		}
		fmt.Printf("%s[github]%s upload attempt %d/%d failed: %s\n", ansiRed, ansiReset, attempt, total, err.Error())
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s[github]%s uploaded %s\n", ansiGreen, ansiReset, downloadURL)
	return nil
}

func uploadCLIExportToEdgetunnel(cfg *cliConfig, content string) error {
	if strings.TrimSpace(cfg.export.ETHost) == "" {
		return fmt.Errorf("启用 -edgetunnel 时需要 -ethost")
	}
	if strings.TrimSpace(cfg.export.ETPassword) == "" {
		return fmt.Errorf("启用 -edgetunnel 时需要 -etpassword")
	}
	mode := cfg.export.ETMode
	if mode == "" {
		mode = "overwrite"
	}
	params := edgetunnelUploadRequest{
		Host:     cfg.export.ETHost,
		Password: cfg.export.ETPassword,
		Content:  content,
		Mode:     mode,
	}
	err := uploadToEdgetunnel(context.Background(), params, func(msg string) {
		fmt.Printf("%s[edgetunnel]%s %s\n", ansiYellow, ansiReset, msg)
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s[edgetunnel]%s uploaded\n", ansiGreen, ansiReset)
	return nil
}


func cliSpeedStatus(value string, speedMin float64) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" || value == "未测速" { return "untested" }
	value = strings.TrimSpace(strings.TrimSuffix(value, "MB/s"))
	speed, err := strconv.ParseFloat(value, 64)
	if err == nil && speed >= speedMin { return "qualified" }
	return "unqualified"
}
