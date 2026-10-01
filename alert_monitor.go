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
	lastAlertTime       time.Time
}

var globalAlertMonitor = &AlertMonitor{}

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
	if metrics.CPUUsage > 95.0 {
		globalAlertMonitor.consecutiveCpuSpikes++
		if globalAlertMonitor.consecutiveCpuSpikes >= 3 {
			// Trigger CPU Alert
			isAlertTriggered = true
			alertPayload = AgentAlertPayload{
				Type:    "cpu_spike",
				Value:   metrics.CPUUsage,
				Message: fmt.Sprintf("CRITICAL: CPU usage has been >95%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", metrics.CPUUsage),
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
	if metrics.RAMUsage > 95.0 {
		globalAlertMonitor.consecutiveRamSpikes++
		if globalAlertMonitor.consecutiveRamSpikes >= 3 {
			// Trigger RAM Alert (only if CPU alert wasn't already triggered)
			if !isAlertTriggered {
				isAlertTriggered = true
				alertPayload = AgentAlertPayload{
					Type:    "ram_spike",
					Value:   metrics.RAMUsage,
					Message: fmt.Sprintf("CRITICAL: RAM usage has been >95%% for 3 consecutive intervals (45 seconds). Current: %.1f%%", metrics.RAMUsage),
					Context: map[string]interface{}{
						"top_processes": metrics.Processes,
					},
				}
			}
			globalAlertMonitor.consecutiveRamSpikes = 0
		}
	} else {
		globalAlertMonitor.consecutiveRamSpikes = 0
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
