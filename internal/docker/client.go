// Package docker provides Docker SDK operations for PolyON.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/rs/zerolog/log"
)

// Client wraps the Docker SDK client.
type Client struct {
	cli *client.Client
}

// New creates a new Docker client. Returns nil instead of error if Docker is not available (K8s environment).
func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Warn().Err(err).Msg("Docker client init failed (expected in K8s environment)")
		return nil, nil
	}
	// Ping to verify Docker is actually reachable
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx); err != nil {
		log.Info().Msg("Docker not reachable — using kubectl exec fallback (K8s environment)")
		cli.Close()
		return nil, nil
	}
	return &Client{cli: cli}, nil
}

// SDK returns the underlying Docker SDK client for advanced operations.
func (d *Client) SDK() *client.Client {
	if d == nil {
		return nil
	}
	return d.cli
}

// ExecCommand runs an arbitrary command inside a container and returns stdout.
// Falls back to kubectl exec if Docker client is not available (K8s environment).
func (d *Client) ExecCommand(containerName string, args ...string) (string, error) {
	if d == nil || d.cli == nil {
		// K8s fallback — map container name to label selector
		label := "app=" + containerName
		return KubectlExecInPod("polyon", label, args)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	execCfg := container.ExecOptions{
		Cmd:          args,
		AttachStdout: true,
		AttachStderr: true,
	}

	exec, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	output, _ := io.ReadAll(resp.Reader)
	clean := stripDockerHeaders(output)

	inspect, err := d.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return string(clean), nil
	}
	if inspect.ExitCode != 0 {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(clean)))
	}
	return strings.TrimSpace(string(clean)), nil
}

// ExecWithStdin runs a command in a container with stdin data and returns stdout.
func (d *Client) ExecWithStdin(containerName string, cmd []string, stdin []byte) (string, error) {
	if d == nil || d.cli == nil {
		return "", fmt.Errorf("docker client not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}
	exec, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}
	resp, err := d.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	// Write stdin
	resp.Conn.Write(stdin)
	resp.CloseWrite()

	// Read output
	output, _ := io.ReadAll(resp.Reader)
	clean := stripDockerHeaders(output)

	inspect, err := d.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return string(clean), nil
	}
	if inspect.ExitCode != 0 {
		return "", fmt.Errorf("exit code %d: %s", inspect.ExitCode, string(clean))
	}
	return strings.TrimSpace(string(clean)), nil
}

// ExecSambaTool runs samba-tool inside the DC container and returns stdout.
// Falls back to kubectl exec if Docker client is not available (K8s environment).
func (d *Client) ExecSambaTool(containerName string, args ...string) (string, error) {
	if d == nil || d.cli == nil {
		// K8s fallback: use kubectl exec
		return d.kubectlExecSambaTool(args...)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := append([]string{"samba-tool"}, args...)
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	exec, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stdout, stderr bytes.Buffer
	// Docker multiplexed stream: read all into one buffer
	output, _ := io.ReadAll(resp.Reader)

	// Strip Docker stream header bytes (8-byte prefix per frame)
	clean := stripDockerHeaders(output)
	stdout.Write(clean)

	// Check exit code
	inspect, err := d.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return stdout.String(), nil // best effort
	}
	if inspect.ExitCode != 0 {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		return "", fmt.Errorf("%s", strings.TrimSpace(errMsg))
	}

	return strings.TrimSpace(stdout.String()), nil
}

// ContainerList returns all polyon- containers with status.
func (d *Client) ContainerList(ctx context.Context) ([]ContainerInfo, error) {
	if d == nil || d.cli == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}

	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	var result []ContainerInfo
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		if !strings.HasPrefix(name, "polyon-") {
			continue
		}
		result = append(result, ContainerInfo{
			Name:   name,
			Image:  c.Image,
			Status: c.Status,
			State:  c.State,
			Ports:  formatPorts(c.Ports),
		})
	}
	return result, nil
}

// ContainerRestart restarts a container by name.
func (d *Client) ContainerRestart(ctx context.Context, name string) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("docker client not initialized")
	}
	timeout := 10
	return d.cli.ContainerRestart(ctx, name, container.StopOptions{Timeout: &timeout})
}

// ContainerStart starts a stopped container by name.
func (d *Client) ContainerStart(ctx context.Context, name string) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("docker client not initialized")
	}
	return d.cli.ContainerStart(ctx, name, container.StartOptions{})
}

// ContainerStop stops a running container by name.
func (d *Client) ContainerStop(ctx context.Context, name string) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("docker client not initialized")
	}
	timeout := 10
	return d.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeout})
}

// ContainerExists checks if a container with given name exists.
func (d *Client) ContainerExists(ctx context.Context, name string) (bool, string) {
	if d == nil || d.cli == nil {
		return false, ""
	}
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return false, ""
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				return true, c.State
			}
		}
	}
	return false, ""
}

// ContainerLogs returns recent logs.
func (d *Client) ContainerLogs(ctx context.Context, name string, tail int) (string, error) {
	if d == nil || d.cli == nil {
		return "", fmt.Errorf("docker client not initialized")
	}
	reader, err := d.cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       fmt.Sprintf("%d", tail),
	})
	if err != nil {
		return "", err
	}
	defer reader.Close()
	data, _ := io.ReadAll(reader)
	return string(stripDockerHeaders(data)), nil
}

// ContainerInspect returns mount info for a container.
// ContainerInspectRaw returns raw inspect JSON for mount detection etc.
func (d *Client) ContainerInspect(ctx context.Context, name string) (*ContainerInspectResult, error) {
	if d == nil || d.cli == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}
	resp, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		return nil, err
	}
	result := &ContainerInspectResult{}
	for _, m := range resp.Mounts {
		result.Mounts = append(result.Mounts, MountInfo{Source: m.Source, Destination: m.Destination})
	}
	return result, nil
}

type ContainerInspectResult struct {
	Mounts []MountInfo
}

type MountInfo struct {
	Source      string
	Destination string
}

// RawInspectResult holds parsed container inspect data for API use.
type RawInspectResult struct {
	Env    []string // environment variables
	Memory int64    // memory limit in bytes (0 = unlimited)
}

// RawInspect returns environment variables and resource limits for a container.
func (d *Client) RawInspect(ctx context.Context, name string) (*RawInspectResult, error) {
	if d == nil || d.cli == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}
	resp, err := d.cli.ContainerInspect(ctx, name)
	if err != nil {
		return nil, err
	}
	result := &RawInspectResult{
		Env:    resp.Config.Env,
		Memory: resp.HostConfig.Memory,
	}
	return result, nil
}

// SystemDfVolumes returns docker system df volume info.
func (d *Client) SystemDfVolumes(ctx context.Context) ([]VolumeInfo, error) {
	if d == nil || d.cli == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}
	du, err := d.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return nil, err
	}
	var vols []VolumeInfo
	for _, v := range du.Volumes {
		if !strings.HasPrefix(v.Name, "polyon_") {
			continue
		}
		vols = append(vols, VolumeInfo{
			Name:       strings.TrimPrefix(v.Name, "polyon_"),
			FullName:   v.Name,
			Driver:     v.Driver,
			Size:       v.UsageData.Size,
			Links:      int(v.UsageData.RefCount),
			Mountpoint: strings.TrimPrefix(v.Mountpoint, "/host_mnt"),
		})
	}
	return vols, nil
}

// ContainerInfo represents basic container info.
type ContainerInfo struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
	State  string `json:"state"`
	Ports  string `json:"ports"`
}

// formatPorts converts Docker port bindings to a deduplicated display string.
func formatPorts(ports []types.Port) string {
	seen := make(map[string]bool)
	var parts []string
	for _, p := range ports {
		if p.PublicPort == 0 {
			continue
		}
		proto := ""
		if p.Type == "udp" {
			proto = "/udp"
		}
		key := fmt.Sprintf("%d->%d%s", p.PublicPort, p.PrivatePort, proto)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, fmt.Sprintf("0.0.0.0:%s", key))
	}
	return strings.Join(parts, ", ")
}

// VolumeInfo represents volume info.
type VolumeInfo struct {
	Name       string `json:"name"`
	FullName   string `json:"fullName"`
	Driver     string `json:"driver"`
	Size       int64  `json:"size"`
	Links      int    `json:"links"`
	Mountpoint string `json:"mountpoint"`
}

// ContainerStatusMap returns a map of container name → status string.
func (d *Client) ContainerStatusMap(ctx context.Context) map[string]string {
	result := map[string]string{}
	containers, err := d.ContainerList(ctx)
	if err != nil {
		return result
	}
	for _, c := range containers {
		result[c.Name] = c.Status
	}
	return result
}

// RunCompose executes docker compose via the docker:cli pattern.
func (d *Client) RunCompose(ctx context.Context, hostProjectDir string, args ...string) error {
	// This uses docker run with docker:cli image, same as Python version
	log.Info().Strs("args", args).Msg("Running compose via docker:cli")
	// Implementation uses exec similar to Python's subprocess approach
	// For now, using docker SDK to create a container that runs compose
	return nil // TODO: implement compose runner
}

// ExecInContainer runs an arbitrary command in a container and returns stdout.
// Similar to ExecCommand but accepts a []string command slice.
func (d *Client) ExecInContainer(containerName string, cmd []string) (string, error) {
	if d == nil || d.cli == nil {
		return "", fmt.Errorf("docker client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}

	exec, err := d.cli.ContainerExecCreate(ctx, containerName, execCfg)
	if err != nil {
		return "", fmt.Errorf("exec create: %w", err)
	}

	resp, err := d.cli.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	output, _ := io.ReadAll(resp.Reader)
	clean := stripDockerHeaders(output)

	inspect, err := d.cli.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return string(clean), nil
	}
	if inspect.ExitCode != 0 {
		return "", fmt.Errorf("%s", strings.TrimSpace(string(clean)))
	}
	return strings.TrimSpace(string(clean)), nil
}

// CopyToContainer copies content to a file inside a container via Docker API.
// content is wrapped in a tar archive before transmission.
func (d *Client) CopyToContainer(containerName, destPath string, content []byte) error {
	if d == nil || d.cli == nil {
		return fmt.Errorf("docker client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build tar archive with a single file
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filepath.Base(destPath),
		Mode: 0644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("tar write: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}

	return d.cli.CopyToContainer(ctx, containerName, filepath.Dir(destPath), &buf, types.CopyToContainerOptions{})
}

// CopyFromContainer reads a file from inside a container via Docker API.
func (d *Client) CopyFromContainer(containerName, srcPath string) ([]byte, error) {
	if d == nil || d.cli == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reader, _, err := d.cli.CopyFromContainer(ctx, containerName, srcPath)
	if err != nil {
		return nil, fmt.Errorf("copy from container: %w", err)
	}
	defer reader.Close()

	// Response is a tar archive — extract the first file
	tr := tar.NewReader(reader)
	_, err = tr.Next()
	if err != nil {
		return nil, fmt.Errorf("tar next: %w", err)
	}
	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("tar read: %w", err)
	}
	return data, nil
}

// kubectlExecSambaTool runs samba-tool via kubectl exec (K8s fallback).
func (d *Client) kubectlExecSambaTool(args ...string) (string, error) {
	cmd := append([]string{"samba-tool"}, args...)
	return KubectlExecInPod("polyon", "app=polyon-dc", cmd)
}

// KubectlExecInPod runs a command in a K8s pod via kubectl exec.
func KubectlExecInPod(namespace, labelSelector string, cmd []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get pod name
	getPod := osexec.CommandContext(ctx, "kubectl", "get", "pods",
		"-n", namespace, "-l", labelSelector,
		"-o", "jsonpath={.items[0].metadata.name}")
	podNameBytes, err := getPod.Output()
	if err != nil {
		return "", fmt.Errorf("kubectl get pod (%s): %w", labelSelector, err)
	}
	podName := strings.TrimSpace(string(podNameBytes))
	if podName == "" {
		return "", fmt.Errorf("no pod found for selector %s", labelSelector)
	}

	execArgs := []string{"exec", podName, "-n", namespace, "--"}
	execArgs = append(execArgs, cmd...)
	execCmd := osexec.CommandContext(ctx, "kubectl", execArgs...)
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr
	if err := execCmd.Run(); err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		return "", fmt.Errorf("%s", strings.TrimSpace(errMsg))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// stripDockerHeaders removes Docker multiplexed stream header bytes.
func stripDockerHeaders(data []byte) []byte {
	var clean []byte
	for len(data) >= 8 {
		// Header: [stream_type(1), 0, 0, 0, size(4)]
		size := int(data[4])<<24 | int(data[5])<<16 | int(data[6])<<8 | int(data[7])
		data = data[8:]
		if size > len(data) {
			size = len(data)
		}
		clean = append(clean, data[:size]...)
		data = data[size:]
	}
	if len(clean) == 0 {
		return data // fallback: not multiplexed
	}
	return clean
}

// Helper for JSON formatting in topology etc.
func ToJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
