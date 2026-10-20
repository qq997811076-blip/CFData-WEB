package main

import "encoding/json"

type wsRequest struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type startTaskRequest struct {
	IPType            int     `json:"ipType"`
	Threads           int     `json:"threads"`
	Port              int     `json:"port"`
	Delay             int     `json:"delay"`
	ScanMode          string  `json:"scanMode"`
	AutoSpeed         bool    `json:"autoSpeed"`
	OfficialTargetDC  string  `json:"officialTargetDC"`
	OfficialSpeedPort int     `json:"officialSpeedPort"`
	OfficialSpeedURL  string  `json:"officialSpeedURL"`
	OfficialSpeedMin  float64 `json:"officialSpeedMin"`
	OfficialSpeedLimit int    `json:"officialSpeedLimit"`
	OfficialSpeedConcurrency int `json:"officialSpeedConcurrency"`
}

type startTestRequest struct {
	DC       string `json:"dc"`
	Port     int    `json:"port"`
	Delay    int    `json:"delay"`
	ScanMode string `json:"scanMode"`
}

type startSpeedTestRequest struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
	URL  string `json:"url"`
}

type startOfficialSpeedBatchRequest struct {
	Port       int          `json:"port"`
	URL        string       `json:"url"`
	SpeedMin   float64      `json:"speedMin"`
	SpeedLimit int          `json:"speedLimit"`
	SpeedConcurrency int    `json:"speedConcurrency"`
	Results    []TestResult `json:"results"`
	SkipTested bool         `json:"skipTested"`
}

type edgetunnelUploadRequest struct {
	Host     string `json:"host"`
	Password string `json:"password"`
	Content  string `json:"content"`
	Mode     string `json:"mode"`
	Silent   bool   `json:"silent"`
}

type githubUploadRequest struct {
	Token   string `json:"token"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
	Path    string `json:"path"`
	Message string `json:"message"`
	Content string `json:"content"`
	Silent  bool   `json:"silent"`
}
