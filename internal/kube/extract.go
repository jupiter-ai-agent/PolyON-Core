package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rs/zerolog/log"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExtractModuleManifest pulls an image via K8s Job and extracts /polyon-module/module.yaml.
func (c *Client) ExtractModuleManifest(ctx context.Context, image string) ([]byte, error) {
	if c == nil || c.cs == nil {
		return nil, fmt.Errorf("k8s client not initialized")
	}

	jobName := fmt.Sprintf("extract-manifest-%d", time.Now().UnixMilli()%100000)

	// Create Job that cats the module.yaml
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: c.namespace,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: int32Ptr(0),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "extract",
							Image:   image,
							Command: []string{"cat", "/polyon-module/module.yaml"},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: int64Ptr(0), // root to read file
							},
						},
					},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	// Create job
	_, err := c.cs.BatchV1().Jobs(c.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create extract job: %w", err)
	}
	defer c.cleanupJob(jobName)

	// Wait for completion (max 120s)
	deadline := time.Now().Add(120 * time.Second)
	var podName string
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		j, err := c.cs.BatchV1().Jobs(c.namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		if j.Status.Succeeded > 0 {
			// Find pod
			pods, err := c.cs.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
				LabelSelector: fmt.Sprintf("job-name=%s", jobName),
			})
			if err == nil && len(pods.Items) > 0 {
				podName = pods.Items[0].Name
			}
			break
		}
		if j.Status.Failed > 0 {
			return nil, fmt.Errorf("extract job failed — image may not contain /polyon-module/module.yaml")
		}
	}

	if podName == "" {
		return nil, fmt.Errorf("extract job timed out")
	}

	// Read pod logs (= module.yaml content)
	req := c.cs.CoreV1().Pods(c.namespace).GetLogs(podName, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("read extract logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return nil, fmt.Errorf("copy extract logs: %w", err)
	}

	log.Info().Str("image", image).Int("bytes", buf.Len()).Msg("module.yaml extracted")
	return buf.Bytes(), nil
}

func (c *Client) cleanupJob(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	propagation := metav1.DeletePropagationBackground
	_ = c.cs.BatchV1().Jobs(c.namespace).Delete(ctx, name, metav1.DeleteOptions{
		PropagationPolicy: &propagation,
	})
}

func int32Ptr(i int32) *int32 { return &i }
func int64Ptr(i int64) *int64 { return &i }
