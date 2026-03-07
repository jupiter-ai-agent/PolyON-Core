package kube

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Client struct {
	cs        *kubernetes.Clientset
	namespace string
}

type PodStatus struct {
	Name   string
	Phase  string
	Ready  bool
	Status string
}

func New() (*Client, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		log.Warn().Err(err).Msg("K8s in-cluster config failed (not in K8s?)")
		return nil, nil
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Warn().Err(err).Msg("K8s clientset creation failed")
		return nil, nil
	}
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err == nil {
			ns = strings.TrimSpace(string(data))
		} else {
			ns = "polyon"
		}
	}
	log.Info().Str("namespace", ns).Msg("K8s client initialized")
	return &Client{cs: cs, namespace: ns}, nil
}

func (c *Client) ListPods(ctx context.Context) ([]PodStatus, error) {
	if c == nil || c.cs == nil {
		return nil, fmt.Errorf("k8s client not initialized")
	}
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := c.cs.CoreV1().Pods(c.namespace).List(ctx2, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var result []PodStatus
	for _, p := range pods.Items {
		ready := true
		for _, cond := range p.Status.Conditions {
			if cond.Type == "Ready" {
				ready = cond.Status == "True"
				break
			}
		}
		status := string(p.Status.Phase)
		if len(p.Status.ContainerStatuses) > 0 {
			cs := p.Status.ContainerStatuses[0]
			if cs.State.Waiting != nil {
				status = cs.State.Waiting.Reason
			}
		}
		result = append(result, PodStatus{Name: p.Name, Phase: string(p.Status.Phase), Ready: ready, Status: status})
	}
	return result, nil
}

func (c *Client) PodStatusMap(ctx context.Context) (map[string]PodStatus, error) {
	pods, err := c.ListPods(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]PodStatus)
	for _, p := range pods {
		base := extractBaseName(p.Name)
		result[base] = p
	}
	return result, nil
}

func extractBaseName(podName string) string {
	parts := strings.Split(podName, "-")
	// StatefulSet: polyon-db-0 → polyon-db
	if len(parts) >= 2 {
		last := parts[len(parts)-1]
		if isNumeric(last) {
			return strings.Join(parts[:len(parts)-1], "-")
		}
	}
	// Deployment: polyon-core-799b869c79-nr7rv → polyon-core
	if len(parts) >= 3 {
		return strings.Join(parts[:len(parts)-2], "-")
	}
	return podName
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}