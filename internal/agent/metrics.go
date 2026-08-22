// Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
// Licensed under the Functional Source License, Version 1.1, ALv2 Future License (FSL-1.1-ALv2).

package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"rasnuos-agent/internal/buildinfo"
	"rasnuos-agent/internal/transport"

	"rasnuos-agent/internal/osadapter"
)

type MetricsPayload struct {
	ServerID     string                    `json:"server_id"`
	AgentVersion string                    `json:"agent_version"`
	CPUUsage     float64                   `json:"cpu_usage"`
	RAMUsage     float64                   `json:"ram_usage"`
	DiskUsage    float64                   `json:"disk_usage"`
	SwapUsage    float64                   `json:"swap_usage"`
	IOWait       float64                   `json:"io_wait"`
	Load5        float64                   `json:"load_5"`
	CPUCores     int                       `json:"cpu_cores"`
	LoadAvg      string                    `json:"load_average"`
	NetworkRX    uint64                    `json:"network_rx"`
	NetworkTX    uint64                    `json:"network_tx"`
	DiskRead     uint64                    `json:"disk_read_kb"`
	DiskWrite    uint64                    `json:"disk_write_kb"`
	Processes    []osadapter.Process       `json:"processes"`
	Services     []osadapter.ServiceStatus `json:"services"`
	OOMKills     []string                  `json:"oom_kills"`
	Errors       []string                  `json:"errors"`
	Timestamp    int64                     `json:"timestamp"`
}

var (
	metricsTickerDuration = 15 * time.Second
	metricsQueue          = make([]MetricsPayload, 0)
	metricsMutex          sync.Mutex
	isFlushing            bool
)

func (c *PusherClient) StartMetricsEngine(shutdownChan chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in StartMetricsEngine: %v", r)
		}
	}()
	ticker := time.NewTicker(metricsTickerDuration)
	defer ticker.Stop()

	log.Println("Metrics engine started.")

	for {
		select {
		case <-shutdownChan:
			log.Println("Metrics engine shutting down.")
			return
		case <-ticker.C:
			go c.collectAndSendMetrics()
		}
	}
}

func (c *PusherClient) collectAndSendMetrics() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in collectAndSendMetrics: %v", r)
		}
	}()

	metrics, adapterErrs := osadapter.Current().GetMetrics()
	var errs []string
	for _, err := range adapterErrs {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	c.checkAlerts(metrics)

	payload := MetricsPayload{
		ServerID:     c.ServerID,
		AgentVersion: buildinfo.Version,
		CPUUsage:     metrics.CPUUsage,
		RAMUsage:     metrics.RAMUsage,
		DiskUsage:    metrics.DiskUsage,
		SwapUsage:    metrics.SwapUsage,
		IOWait:       metrics.IOWait,
		Load5:        metrics.Load5,
		CPUCores:     metrics.CPUCores,
		LoadAvg:      metrics.LoadAvg,
		NetworkRX:    metrics.NetworkRX,
		NetworkTX:    metrics.NetworkTX,
		DiskRead:     metrics.DiskRead,
		DiskWrite:    metrics.DiskWrite,
		Processes:    metrics.Processes,
		Services:     metrics.Services,
		OOMKills:     metrics.OOMKills,
		Errors:       errs,
		Timestamp:    time.Now().Unix(),
	}

	metricsMutex.Lock()
	metricsQueue = append(metricsQueue, payload)
	if len(metricsQueue) > 1000 {
		metricsQueue = metricsQueue[1:]
	}

	// Retain full process/service snapshots only on the latest queued entry; initialize
	// empty slices so JSON marshals as [] rather than null.
	if len(metricsQueue) > 1 {
		metricsQueue[len(metricsQueue)-2].Processes = make([]osadapter.Process, 0)
		metricsQueue[len(metricsQueue)-2].Services = make([]osadapter.ServiceStatus, 0)
		metricsQueue[len(metricsQueue)-2].OOMKills = make([]string, 0)
	}

	if !isFlushing {
		isFlushing = true
		go c.flushMetricsQueue()
	}
	metricsMutex.Unlock()
}

func (c *PusherClient) flushMetricsQueue() {
	defer func() {
		metricsMutex.Lock()
		isFlushing = false
		metricsMutex.Unlock()
	}()

	for {
		metricsMutex.Lock()
		if len(metricsQueue) == 0 {
			metricsMutex.Unlock()
			return
		}
		payload := metricsQueue[0]
		metricsMutex.Unlock()

		url := fmt.Sprintf("%s/agent/metrics", c.ApiURL)
		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Printf("Metrics JSON error: %v", err)
			return
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Token)

		resp, err := transport.Heartbeat.Do(req)

		if err != nil || resp.StatusCode >= 500 {
			if err != nil {
				log.Printf("Failed to send metrics: %v", err)
			}
			if resp != nil {
				resp.Body.Close()
			}
			return
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			log.Printf("Metrics dropped due to client error HTTP %d", resp.StatusCode)
		}

		resp.Body.Close()

		metricsMutex.Lock()
		if len(metricsQueue) > 0 {
			metricsQueue = metricsQueue[1:]
		}
		metricsMutex.Unlock()

		time.Sleep(250 * time.Millisecond)
	}
}
