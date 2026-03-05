package builder

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/rs/zerolog/log"
)

const buildImage = "node:20-alpine"

// ensureBuildImage pulls the build image if not present.
func (b *Builder) ensureBuildImage(ctx context.Context, sdk *client.Client) error {
	images, err := sdk.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return err
	}
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == buildImage {
				return nil
			}
		}
	}

	log.Info().Str("image", buildImage).Msg("pulling build image")
	reader, err := sdk.ImagePull(ctx, "docker.io/library/"+buildImage, image.PullOptions{})
	if err != nil {
		return err
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

// execInBuildContainer runs a command in a persistent build container.
func (b *Builder) execInBuildContainer(ctx context.Context, containerName, cmd, workDir string) (string, error) {
	sdk := b.docker.SDK()
	if sdk == nil {
		return "", fmt.Errorf("docker not available")
	}

	if err := b.ensureBuildImage(ctx, sdk); err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	// Check if container exists
	_, err := sdk.ContainerInspect(ctx, containerName)
	if err != nil {
		// Create container
		resp, err := sdk.ContainerCreate(ctx,
			&container.Config{
				Image:      buildImage,
				Cmd:        []string{"sleep", "7200"},
				WorkingDir: "/workspace",
				Env: []string{
					"NODE_OPTIONS=--max-old-space-size=8192",
					"CI=true",
				},
			},
			&container.HostConfig{
				Mounts: []mount.Mount{
					{
						Type:   mount.TypeVolume,
						Source: "polyon-sites-data",
						Target: "/data/sites",
					},
				},
				Resources: container.Resources{
					Memory:   8 * 1024 * 1024 * 1024, // 8GB
					NanoCPUs: 4 * 1e9,                 // 4 CPUs
				},
			},
			&network.NetworkingConfig{},
			nil,
			containerName,
		)
		if err != nil {
			return "", fmt.Errorf("create container: %w", err)
		}
		if err := sdk.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
			return "", fmt.Errorf("start container: %w", err)
		}
		log.Info().Str("container", containerName).Msg("build container started")

		// Install git in alpine
		_, _ = b.execCmd(ctx, sdk, containerName, "apk add --no-cache git openssh 2>&1")
	}

	return b.execCmd(ctx, sdk, containerName, cmd)
}

func (b *Builder) execCmd(ctx context.Context, sdk *client.Client, containerName, cmd string) (string, error) {
	exec, err := sdk.ContainerExecCreate(ctx, containerName, container.ExecOptions{
		Cmd:          []string{"sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}

	resp, err := sdk.ContainerExecAttach(ctx, exec.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer resp.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, resp.Reader)

	inspect, err := sdk.ContainerExecInspect(ctx, exec.ID)
	if err != nil {
		return buf.String(), err
	}
	if inspect.ExitCode != 0 {
		return buf.String(), fmt.Errorf("exit code %d", inspect.ExitCode)
	}

	return buf.String(), nil
}

// removeBuildContainer stops and removes the build container.
func (b *Builder) removeBuildContainer(ctx context.Context, containerName string) {
	sdk := b.docker.SDK()
	if sdk == nil {
		return
	}
	_ = sdk.ContainerStop(ctx, containerName, container.StopOptions{})
	_ = sdk.ContainerRemove(ctx, containerName, container.RemoveOptions{Force: true})
	log.Info().Str("container", containerName).Msg("build container removed")
}
