package mcp

import (
	"context"
	"io"

	otypes "github.com/obot-platform/obot/apiclient/types"
)

// cfInternalDomain is the Cloud Foundry internal domain. Routes on it resolve to
// container IPs in 10.255.0.0/16 and are reachable only from inside the space,
// and only where a container-to-container network policy permits it.
//
// The domain is reserved by the platform: an operator cannot point it at an
// arbitrary host, and the Cloud Controller will not create a route on it for a
// hostname another space owns. That is what makes allow-listing the whole
// suffix narrow rather than broad.
const (
	cfInternalDomain  = "apps.internal"
	cfInternalMCPPort = "8080"
)

// cloudFoundryBackend runs Obot as a Cloud Foundry application without a
// container-orchestration runtime for MCP servers.
//
// Cloud Foundry has no equivalent of the docker or Kubernetes runtimes that the
// other backends drive, so this backend supports exactly the runtimes that need
// no orchestration: RuntimeRemote and RuntimeVMCP, which are served by Obot's
// own gateway process. Every runtime that requires Obot to start a workload
// (uvx, npx, containerized) reports ErrNotSupportedByBackend, which the API
// handlers translate into a 404 rather than a 500.
//
// Deploying MCP servers as Cloud Foundry applications through the Cloud
// Controller v3 API is deliberately out of scope here; streamServerLogs,
// restartServer, and getServerDetails are the seams that work would grow into.
// Until then, MCP servers are deployed as their own Cloud Foundry apps on the
// internal domain and registered as RuntimeRemote -- see remoteConfig.
type cloudFoundryBackend struct {
	authEnabled    bool
	httpListenPort int
}

func newCloudFoundryBackend(authEnabled bool, httpListenPort int, _ Options) backend {
	return &cloudFoundryBackend{
		authEnabled:    authEnabled,
		httpListenPort: httpListenPort,
	}
}

// runtimeIsGatewayServed reports whether a runtime is served by Obot's own
// process and therefore needs no deployment of any kind. This mirrors the
// docker and Kubernetes backends, which short-circuit the same two runtimes
// before touching their orchestrator. RuntimeComposite is deliberately absent:
// upstream retains it only to identify legacy resources during migration, and
// the compositemigration controller rewrites those into vmcp servers.
func runtimeIsGatewayServed(runtime otypes.Runtime) bool {
	return runtime == otypes.RuntimeRemote || runtime == otypes.RuntimeVMCP
}

func (c *cloudFoundryBackend) ensureServerDeployment(_ context.Context, server ServerConfig) (ServerConfig, error) {
	for i, component := range server.Components {
		component.URL = c.transformObotHostname(component.URL)
		server.Components[i] = component
	}

	for i, webhook := range server.Webhooks {
		webhook.URL = c.transformObotHostname(webhook.URL)
		server.Webhooks[i] = webhook
	}

	if runtimeIsGatewayServed(server.Runtime) {
		return server, nil
	}

	return ServerConfig{}, &ErrNotSupportedByBackend{
		Feature: "deploying " + string(server.Runtime) + " MCP servers",
		Backend: RuntimeBackendCloudFoundry,
	}
}

func (c *cloudFoundryBackend) deployServer(_ context.Context, server ServerConfig) error {
	if runtimeIsGatewayServed(server.Runtime) {
		return nil
	}

	return &ErrNotSupportedByBackend{
		Feature: "deploying " + string(server.Runtime) + " MCP servers",
		Backend: RuntimeBackendCloudFoundry,
	}
}

func (c *cloudFoundryBackend) getServerDetails(_ context.Context, _ string) (otypes.MCPServerDetails, error) {
	return otypes.MCPServerDetails{}, &ErrNotSupportedByBackend{
		Feature: "server details",
		Backend: RuntimeBackendCloudFoundry,
	}
}

func (c *cloudFoundryBackend) streamServerLogs(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, &ErrNotSupportedByBackend{
		Feature: "server logs",
		Backend: RuntimeBackendCloudFoundry,
	}
}

func (c *cloudFoundryBackend) restartServer(_ context.Context, _ ServerConfig) error {
	return &ErrNotSupportedByBackend{
		Feature: "restarting servers",
		Backend: RuntimeBackendCloudFoundry,
	}
}

// shutdownServer must succeed for every runtime, including the ones this backend
// cannot deploy. It is called from delete and finalizer paths as well as the
// idle-disable path, and returning an error there wedges the finalizer instead
// of degrading. Nothing is ever deployed, so there is nothing to shut down.
func (c *cloudFoundryBackend) shutdownServer(_ context.Context, _ string, _ bool) error {
	return nil
}

// transformObotHostname is the identity function. Obot is reachable at its
// external route, and no MCP workload runs anywhere that needs a rewritten
// hostname. Routing over the Cloud Foundry internal domain would change this.
func (c *cloudFoundryBackend) transformObotHostname(url string) string {
	return url
}

// remoteConfig keeps the operator's address-range policy untouched and adds the
// Cloud Foundry internal domain and MCP listener port to the allow list.
//
// MCP servers that this backend cannot deploy (containerized, uvx, npx) are
// instead deployed as their own Cloud Foundry apps with a route on
// apps.internal, no public route, and a container-to-container network policy
// admitting only the gateway. They are then registered as RuntimeRemote.
//
// Those routes resolve to 10.255.0.0/16, which is RFC1918, so Obot's default
// DisallowPrivateIPMCP would reject them -- at admission, before any of the
// network plumbing is exercised. Allow-listing the suffix on port 8080 is the
// narrow fix:
//
//   - Private, loopback, and link-local blocking all remain in force for every
//     other host, which is what protects operator- and partner-supplied remote
//     URLs from being used for SSRF against the platform's internal network.
//   - apps.internal is platform-reserved and space-scoped. A route on it cannot
//     be created for a hostname another space owns, so the suffix cannot be
//     used to reach anything the gateway was not deliberately given a network
//     policy for.
//
// The alternative -- setting DisallowPrivateIPMCP=false -- would unblock the
// entire RFC1918 space for every remote server, including partner-supplied
// URLs. That is a much larger hole than the one being opened here.
//
// The flags are returned unwidened on purpose: unlike the docker backend, this
// one does not need private-IP blocking relaxed wholesale, because every host
// it legitimately reaches is nameable.
func (c *cloudFoundryBackend) remoteConfig(globalConfig RemoteMCPURLValidationConfig) (RemoteMCPURLValidationConfig, []string) {
	return globalConfig, []string{"*." + cfInternalDomain + ":" + cfInternalMCPPort}
}
