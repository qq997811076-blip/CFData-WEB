package main

import (
	"context"
	"embed"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	requestURL       = "speed.cloudflare.com/cdn-cgi/trace"
	scanModeTCPing   = "tcping"
	scanModeHTTPing  = "httping"
)

func latencyMultiplier(scanMode string, isTLS bool) float64 {
	if scanMode != scanModeHTTPing {
		return 1.0
	}
	if isTLS {
		return 4.0
	}
	return 1.3
}

const (
	timeout     = 3 * time.Second
	maxDuration = 5 * time.Second
)

var (
	//go:embed index.html login.html favicon.png
	staticFiles     embed.FS
	customDNSServer string
	customDNSForced bool
	customResolver  *net.Resolver
)

const defaultDNSServers = "223.5.5.5,8.8.8.8"

type location struct {
	Iata      string  `json:"iata"`
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Cca2      string  `json:"cca2"`
	Region    string  `json:"region"`
	City      string  `json:"city"`
	Region_zh string  `json:"region_zh"`
	Country   string  `json:"country"`
	City_zh   string  `json:"city_zh"`
	Emoji     string  `json:"emoji"`
}

type DataCenterInfo struct {
	DataCenter string
	DCCountry  string
	City       string
	IPCount    int
	MinLatency int
}

type ScanResult struct {
	IP          string
	Port        int
	DataCenter  string
	DCCountry   string
	Region      string
	City        string
	LatencyStr  string
	TCPDuration time.Duration
}

type TestResult struct {
	IP         string
	Port       int
	DataCenter string
	DCCountry  string
	Region     string
	City       string
	MinLatency time.Duration
	MaxLatency time.Duration
	AvgLatency time.Duration
	LossRate   float64
	Speed      string
}

type iptestResult struct {
	ipAddr         string
	originalInput  string
	port           int
	dataCenter     string
	locCode        string
	region         string
	city           string
	latency        string
	lossRate       float64
	tcpDuration    time.Duration
	outboundIP     string
	ipType         string
	asnNumber      string
	asnOrg         string
	visitScheme    string
	tlsVersion     string
	sni            string
	httpVersion    string
	warp           string
	gateway        string
	rbi            string
	kex            string
	timestamp      string
	downloadSpeed  float64
	speedText      string
	speedTested    bool
	speedQualified bool
}

type csvHeaderPayload struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	File    string     `json:"file"`
}

type resetConfigResult struct {
	Success  bool     `json:"success"`
	Deleted  []string `json:"deleted"`
	Missing  []string `json:"missing"`
	Failed   []string `json:"failed"`
	Reminder string   `json:"reminder"`
}

type appSession struct {
	ws                   *websocket.Conn
	emit                 func(msgType string, data interface{})
	wsMutex              sync.Mutex
	taskMutex            sync.Mutex
	isTaskRunning        bool
	taskCancel           context.CancelFunc
	backgroundTask       bool
	backgroundSnapshot   backgroundTaskSnapshot
	wsClosed             bool
	scanMutex            sync.Mutex
	scanResults          []ScanResult
	testMutex            sync.Mutex
	testResults          []TestResult
	progressMutex        sync.Mutex
	progressState        map[string][2]int
	progressPrintTime    map[string]time.Time
	progressPrintPercent map[string]float64
}

type backgroundTaskSnapshot struct {
	Label          string                 `json:"label"`
	Mode           string                 `json:"mode"`
	Phase          string                 `json:"phase"`
	Message        string                 `json:"message"`
	Current        int                    `json:"current"`
	Total          int                    `json:"total"`
	Percent        float64                `json:"percent"`
	ResultCount    int                    `json:"resultCount"`
	SpeedCount     int                    `json:"speedCount"`
	ScanTotal      int                    `json:"scanTotal"`
	ScanSuccess    int                    `json:"scanSuccess"`
	ScanFailed     int                    `json:"scanFailed"`
	SpeedTotal     int                    `json:"speedTotal"`
	SpeedSuccess   int                    `json:"speedSuccess"`
	SpeedFailed    int                    `json:"speedFailed"`
	SpeedQualified int                    `json:"speedQualified"`
	TestCount      int                    `json:"testCount"`
	DCCount        int                    `json:"dcCount"`
	DCInfo         string                 `json:"dcInfo,omitempty"`
	Running        bool                   `json:"running"`
	StartedAt      time.Time              `json:"startedAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
	Params         map[string]interface{} `json:"params,omitempty"`
}

type taskStarter func(ctx context.Context, session *appSession)

var (
	locationMap map[string]location

	upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	configResetMutex sync.Mutex

	listenPort       int
	listenHost       string
	speedTestURL     string
	speedTestWorkers = 5
	debugMode        bool
	debugLevel       = "error"
	skipGeoCheck     bool
)
