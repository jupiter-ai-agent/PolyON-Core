// Package monitor provides the health checker goroutine.
package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/config"
	"github.com/triangles/polyon-core/internal/docker"
	"github.com/triangles/polyon-core/internal/store"
)

const checkInterval = 60 * time.Second

var (
	firedAlerts = sync.Map{}
	stopCh      = make(chan struct{})
)

// Start begins the health checker goroutine. Only one instance runs.
func Start(dc *docker.Client, st *store.Store) {
	go func() {
		log.Info().Msg("[HealthChecker] Started (interval: 60s)")
		// Wait for services to boot
		time.Sleep(30 * time.Second)

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				checkPrometheusAlerts(st)
				checkContainerHealth(dc, st)
			case <-stopCh:
				log.Info().Msg("[HealthChecker] Stopped")
				return
			}
		}
	}()
}

// Stop stops the health checker.
func Stop() {
	close(stopCh)
}

func checkPrometheusAlerts(st *store.Store) {
	if st == nil {
		return
	}
	prometheusURL := "http://polyon-prometheus:9090" // fallback
	if cfg := config.Get(); cfg != nil && cfg.PrometheusURL != "" {
		prometheusURL = cfg.PrometheusURL
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(prometheusURL + "/prometheus/api/v1/alerts")
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var data struct {
		Data struct {
			Alerts []struct {
				State       string            `json:"state"`
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"alerts"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&data)

	currentFiring := map[string]bool{}
	for _, a := range data.Data.Alerts {
		if a.State != "firing" {
			continue
		}
		alertName := a.Labels["alertname"]
		severity := a.Labels["severity"]
		service := a.Labels["job"]
		if service == "" {
			service = a.Labels["service"]
		}
		if service == "" {
			service = "infrastructure"
		}
		summary := a.Annotations["summary"]
		if summary == "" {
			summary = alertName
		}

		key := alertName + ":" + a.Labels["instance"]
		currentFiring[key] = true

		if _, already := firedAlerts.Load(key); !already {
			level := "WARN"
			if severity == "critical" {
				level = "CRITICAL"
			}
			st.CreateAlert(level, service, summary, "prometheus",
				map[string]interface{}{
					"alertname":   alertName,
					"description": a.Annotations["description"],
					"instance":    a.Labels["instance"],
					"severity":    severity,
				}, nil)
			firedAlerts.Store(key, true)
			log.Info().Str("level", level).Str("service", service).Str("msg", summary).Msg("[HealthChecker] Alert")
		}
	}

	// Clear resolved
	firedAlerts.Range(func(key, _ interface{}) bool {
		if !currentFiring[key.(string)] {
			firedAlerts.Delete(key)
		}
		return true
	})
}

func checkContainerHealth(dc *docker.Client, st *store.Store) {
	if dc == nil || st == nil {
		return
	}
	containers, err := dc.ContainerList(context.Background())
	if err != nil {
		return
	}

	for _, c := range containers {
		key := "container_down:" + c.Name
		if c.State != "running" {
			if _, already := firedAlerts.Load(key); !already {
				st.CreateAlert("CRITICAL", "docker",
					"컨테이너 '"+c.Name+"' 중지됨 ("+c.Status+")",
					"health_checker",
					map[string]interface{}{"container": c.Name, "state": c.State}, nil)
				firedAlerts.Store(key, true)
			}
		} else if strings.Contains(strings.ToLower(c.Status), "unhealthy") {
			if _, already := firedAlerts.Load(key); !already {
				st.CreateAlert("WARN", "docker",
					"컨테이너 '"+c.Name+"' unhealthy",
					"health_checker",
					map[string]interface{}{"container": c.Name, "status": c.Status}, nil)
				firedAlerts.Store(key, true)
			}
		} else {
			firedAlerts.Delete(key)
		}
	}
}
