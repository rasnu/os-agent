/**
 * Project: RasnuOne
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"rasnuone-agent/os_adapter"
)

type MetricsPayload struct {
	ServerID     string                     `json:"server_id"`
	AgentVersion string                     `json:"agent_version"`
	CPUUsage     float64                    `json:"cpu_usage"`
	RAMUsage     float64                    `json:"ram_usage"`
	DiskUsage    float64                    `json:"disk_usage"`
	SwapUsage    float64                    `json:"swap_usage"`
	LoadAvg      string                     `json:"load_average"`
	NetworkRX    uint64                     `json:"network_rx"`
	NetworkTX    uint64                     `json:"network_tx"`
	DiskRead     uint64                     `json:"disk_read_kb"`
	DiskWrite    uint64                     `json:"disk_write_kb"`
	Processes    []os_adapter.Process       `json:"processes"`
	Services     []os_adapter.ServiceStatus `json:"services"`
	Errors       []string                   `json:"errors"`
	Timestamp    int64                      `json:"timestamp"`
}

var (
	metricsTickerDuration = 15 * time.Second
	metricsQueue          = make([]MetricsPayload, 0)
	metricsMutex          sync.Mutex
	isFlushing            bool
)

// StartMetricsEngine periodically collects and sends system metrics
func (c *PusherClient) StartMetricsEngine(shutdownChan chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("PANIC RECOVERED in StartMetricsEngine: %v", r)
		}
	}()
	ticker := time.NewTicker(metricsTickerDuration) // Send metrics periodically
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

	metrics, adapterErrs := os_adapter.Current().GetMetrics()
	var errs []string
	for _, err := range adapterErrs {
		if err != nil {
			errs = append(errs, err.Error())
		}
	}

	payload := MetricsPayload{
		ServerID:     c.ServerID,
		AgentVersion: version,
		CPUUsage:     metrics.CPUUsage,
		RAMUsage:     metrics.RAMUsage,
		DiskUsage:    metrics.DiskUsage,
		SwapUsage:    metrics.SwapUsage,
		LoadAvg:      metrics.LoadAvg,
		NetworkRX:    metrics.NetworkRX,
		NetworkTX:    metrics.NetworkTX,
		DiskRead:     metrics.DiskRead,
		DiskWrite:    metrics.DiskWrite,
		Processes:    metrics.Processes,
		Services:     metrics.Services,
		Errors:       errs,
		Timestamp:    time.Now().Unix(),
	}

	metricsMutex.Lock()
	metricsQueue = append(metricsQueue, payload)
	if len(metricsQueue) > 1000 {
		metricsQueue = metricsQueue[1:] // Ring buffer, drop oldest
	}

	// Memory Optimization: Scrub full process/service arrays from older queued items.
	// Only the most recent metric needs to send the live process list to Laravel.
	// This reduces a 100MB RAM explosion down to just 100KB during long outages!
	// We use make() instead of nil so JSON marshals as [] instead of null (prevents JS frontend crashes).
	if len(metricsQueue) > 1 {
		metricsQueue[len(metricsQueue)-2].Processes = make([]os_adapter.Process, 0)
		metricsQueue[len(metricsQueue)-2].Services = make([]os_adapter.ServiceStatus, 0)
	}

	// Only spawn flush goroutine if not already flushing
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

		resp, err := heartbeatHTTPClient.Do(req)

		// If network failure or 5xx server error, we abort flushing and try again later.
		if err != nil || resp.StatusCode >= 500 {
			if err != nil {
				log.Printf("Failed to send metrics: %v", err)
			}
			if resp != nil {
				resp.Body.Close()
			}
			return // Offline or backend error, stop flushing and keep in queue
		}

		// If 4xx client error, it will never succeed, log it and allow it to be dropped.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			log.Printf("Metrics dropped due to client error HTTP %d", resp.StatusCode)
		}

		resp.Body.Close()

		// Success (or 4xx drop)! Remove the sent payload from the queue
		metricsMutex.Lock()
		if len(metricsQueue) > 0 {
			metricsQueue = metricsQueue[1:]
		}
		metricsMutex.Unlock()

		// Wait slightly to prevent overwhelming the Laravel API or frontend with 1000 rapid broadcasts
		time.Sleep(250 * time.Millisecond)
	}
}

func parseTSV(tsv string) []map[string]string {
	lines := strings.Split(strings.TrimSpace(tsv), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return []map[string]string{}
	}
	headers := strings.Split(lines[0], "\t")
	var results []map[string]string
	for i := 1; i < len(lines); i++ {
		parts := strings.Split(lines[i], "\t")
		row := make(map[string]string)
		for j, h := range headers {
			if j < len(parts) {
				row[h] = parts[j]
			} else {
				row[h] = ""
			}
		}
		results = append(results, row)
	}
	return results
}
