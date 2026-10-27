package sandbox

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/opencontainers/image-spec/specs-go/v1"
)

// DockerClient is the subset of the Docker API used by Manager.
type DockerClient interface {
	Ping(context.Context) (types.Ping, error)
	ContainerCreate(context.Context, *container.Config, *container.HostConfig, *network.NetworkingConfig, *v1.Platform, string) (container.CreateResponse, error)
	ContainerStart(context.Context, string, container.StartOptions) error
	ContainerExecCreate(context.Context, string, container.ExecOptions) (container.ExecCreateResponse, error)
	ContainerExecAttach(context.Context, string, container.ExecAttachOptions) (types.HijackedResponse, error)
	ContainerExecInspect(context.Context, string) (container.ExecInspect, error)
	ContainerStop(context.Context, string, container.StopOptions) error
	ContainerRemove(context.Context, string, container.RemoveOptions) error
}
