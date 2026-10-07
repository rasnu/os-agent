/**
 * Project: RasnuOS
 * Copyright © 2026 Rasnu LLC (rasnu.com). All rights reserved.
 */
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"rasnuos-agent/os_adapter"
)

type AlertMonitor struct {
	mu                  sync.Mutex
	consecutiveCpuSpikes int
	consecutiveRamSpikes int
	consecutiveDiskSpikes int
	consecutiveSwapSpikes int
	consecutiveIOWaitSpikes int
	consecutiveLoadSpikes int
	lastOOMMessage      string
	lastAlertTime       time.Time

	thresholds AlertThresholds
}

type AlertThresholds struct {
	CPU           float64 `json:"cpu"`
	RAM           float64 `json:"ram"`
	Disk          float64 `json:"disk"`
	Swap          float64 `json:"swap"`
	IOWait        float64 `json:"iowait"`
	LoadMultiplier float64 `json:"load_multiplier"`
}

var globalAlertMonitor = &AlertMonitor{
	thresholds: AlertThresholds{
		CPU:           95.0,
		RAM:           95.0,
		Disk:          90.0,
		Swap:          80.0,
		IOWait:        50.0,
		LoadMultiplier: 2.0,
	},
}

func UpdateAlertThresholds(raw map[string]interface{}) {
	globalAlertMonitor.mu.Lock()
	defer globalAlertMonitor.mu.Unlock()

	if val, ok := raw["cpu"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.CPU = val
	}
	if val, ok := raw["ram"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.RAM = val
	}
	if val, ok := raw["disk"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.Disk = val
	}
	if val, ok := raw["swap"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.Swap = val
	}
	if val, ok := raw["iowait"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.IOWait = val
	}
	if val, ok := raw["load_multiplier"].(float64); ok && val > 0 {
		globalAlertMonitor.thresholds.LoadMultiplier = val
	}
	
	log.Printf("Applied new alert thresholds: %+v", globalAlertMonitor.thresholds)
}

type AgentAlertPayload struct {
	Type    string                 `json:"type"`
	Value   float64                `json:"value"`
	Message string                 `json:"message"`
	Context map[string]interface{} `json:"context,omitempty"`
}

func (c *PusherClient) checkAlerts(metrics os_adapter.Metrics) {
	globalAlertMonitor.mu.Lock()
	defer globalAlertMonitor.mu.Unlock()

	isAlertTriggered := false
	var alertPayload AgentAlertPayload

	// --- CPU Spike Detection ---
	if metrics.CPUUsage > globalAlertMonitor.thresholds.CPU {
		globalAlertMonitor.consecutiveCpuSpikes++
		if globalAlertMonitor.consecutiveCpuSpikes >= 3 {
			// Trigger CPU Alert
			isAlertTriggered = true
			alertPayload = AgentAlertPayload{
				Type:    "cpu_spike",
				Value:   metrics.CPUUsage,
				Message: fmt.Sprintf("CRITICAL: CPU usage has been >%.0f%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", globalAlertMonitor.thresholds.CPU, metrics.CPUUsage),
				Context: map[string]interface{}{
					"top_processes": metrics.Processes, // The backend AI can read this directly
				},
			}
			globalAlertMonitor.consecutiveCpuSpikes = 0 // Reset
		}
	} else {
		globalAlertMonitor.consecutiveCpuSpikes = 0
	}

	// --- RAM Spike Detection ---
	if metrics.RAMUsage > globalAlertMonitor.thresholds.RAM {
		globalAlertMonitor.consecutiveRamSpikes++
		if globalAlertMonitor.consecutiveRamSpikes >= 3 {
			// Trigger RAM Alert (only if CPU alert wasn't already triggered)
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "ram_spike",
					Value:   metrics.RAMUsage,
					Message: fmt.Sprintf("CRITICAL: RAM usage has been >%.0f%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", globalAlertMonitor.thresholds.RAM, metrics.RAMUsage),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
				globalAlertMonitor.consecutiveRamSpikes = 0
			}
		}
	} else {
		globalAlertMonitor.consecutiveRamSpikes = 0
	}

	// --- Disk Usage Alert Detection ---
	if metrics.DiskUsage > globalAlertMonitor.thresholds.Disk {
		globalAlertMonitor.consecutiveDiskSpikes++
		if globalAlertMonitor.consecutiveDiskSpikes >= 3 {
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "disk_full",
					Value:   metrics.DiskUsage,
					Message: fmt.Sprintf("CRITICAL: Disk usage has exceeded %.0f%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", globalAlertMonitor.thresholds.Disk, metrics.DiskUsage),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
				globalAlertMonitor.consecutiveDiskSpikes = 0
			}
		}
	} else {
		globalAlertMonitor.consecutiveDiskSpikes = 0
	}

	// --- Swap Pressure Alert Detection ---
	if metrics.SwapUsage > globalAlertMonitor.thresholds.Swap {
		globalAlertMonitor.consecutiveSwapSpikes++
		if globalAlertMonitor.consecutiveSwapSpikes >= 3 {
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "swap_pressure",
					Value:   metrics.SwapUsage,
					Message: fmt.Sprintf("CRITICAL: Swap usage has exceeded %.0f%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", globalAlertMonitor.thresholds.Swap, metrics.SwapUsage),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
				globalAlertMonitor.consecutiveSwapSpikes = 0
			}
		}
	} else {
		globalAlertMonitor.consecutiveSwapSpikes = 0
	}

	// --- IO Wait Alert Detection ---
	if metrics.IOWait > globalAlertMonitor.thresholds.IOWait {
		globalAlertMonitor.consecutiveIOWaitSpikes++
		if globalAlertMonitor.consecutiveIOWaitSpikes >= 3 {
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "io_wait",
					Value:   metrics.IOWait,
					Message: fmt.Sprintf("CRITICAL: IO Wait has exceeded %.0f%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", globalAlertMonitor.thresholds.IOWait, metrics.IOWait),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
				globalAlertMonitor.consecutiveIOWaitSpikes = 0
			}
		}
	} else {
		globalAlertMonitor.consecutiveIOWaitSpikes = 0
	}

	// --- Load Average Alert Detection ---
	// Alert if 5-min Load is greater than CPU core count * LoadMultiplier
	if metrics.CPUCores > 0 && metrics.Load5 > float64(metrics.CPUCores)*globalAlertMonitor.thresholds.LoadMultiplier {
		globalAlertMonitor.consecutiveLoadSpikes++
		if globalAlertMonitor.consecutiveLoadSpikes >= 3 {
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "high_load",
					Value:   metrics.Load5,
					Message: fmt.Sprintf("CRITICAL: 5-minute Load Average (%.2f) has exceeded %.1fx CPU core capacity (%d cores) for 3 consecutive intervals.", metrics.Load5, globalAlertMonitor.thresholds.LoadMultiplier, metrics.CPUCores),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
				globalAlertMonitor.consecutiveLoadSpikes = 0
			}
		}
	} else {
		globalAlertMonitor.consecutiveLoadSpikes = 0
	}

	// --- Service Crash Alert Detection ---
	// No debouncing needed for crashed services; if it's failed, it's dead now.
	for _, srv := range metrics.Services {
		if srv.Status == "failed" {
			// Overwrite existing resource alerts because a service crash is a higher priority P0 incident
			isAlertTriggered = true
			alertPayload = AgentAlertPayload{
				Type:    "service_crash",
				Value:   100.0, // binary state
				Message: fmt.Sprintf("CRITICAL: Service '%s' has unexpectedly crashed (Status: failed).", srv.Name),
				Context: map[string]interface{}{
					"service_name": srv.Name,
				},
			}
			break // Only trigger one alert per cycle; backend AI will discover if multiple are down
		}
	}

	// --- OOM Killer Alert Detection ---
	// Out-Of-Memory kills are fatal kernel-level events. No debouncing needed.
	// OOM takes ABSOLUTE highest priority because it is often the root cause of service crashes and CPU spikes.
	if len(metrics.OOMKills) > 0 {
		latestOOM := metrics.OOMKills[len(metrics.OOMKills)-1]
		if latestOOM != globalAlertMonitor.lastOOMMessage {
			// Overwrite any existing alert because OOM is more important
			isAlertTriggered = true
			alertPayload = AgentAlertPayload{
				Type:    "oom_kill",
				Value:   100.0,
				Message: fmt.Sprintf("CRITICAL: Out-Of-Memory (OOM) Kill detected! OS terminated a process: %s", latestOOM),
				Context: map[string]interface{}{
					"oom_logs": metrics.OOMKills,
				},
			}
			// Only mark it as seen when we actually queue it for dispatch!
			globalAlertMonitor.lastOOMMessage = latestOOM
		}
	}

	// --- Dispatch Alert ---
	if isAlertTriggered {
		// 15-minute cooldown for all alerts to prevent spam
		if time.Since(globalAlertMonitor.lastAlertTime) >= 15*time.Minute {
			globalAlertMonitor.lastAlertTime = time.Now()
			go c.sendSystemAlert(alertPayload)
		} else {
			log.Printf("Alert Triggered (%s) but suppressed due to 15-minute cooldown.", alertPayload.Type)
		}
	}
}

func (c *PusherClient) sendSystemAlert(payload AgentAlertPayload) {
	url := fmt.Sprintf("%s/agent/alert", c.ApiURL)
	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Alert JSON error: %v", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)

	log.Printf("🚨 Dispatching system alert to backend: %s (%.1f%%)", payload.Type, payload.Value)
	
	resp, err := heartbeatHTTPClient.Do(req)
	if err != nil {
		log.Printf("Failed to send system alert: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("System alert rejected by backend: HTTP %d", resp.StatusCode)
	} else {
		log.Printf("System alert successfully delivered. Backend may initiate autonomous healing.")
	}
}
