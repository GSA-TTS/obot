package mcp

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	otypes "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/safehttp"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

var (
	// gatewayServedRuntimes are the runtimes Obot's own process serves, which
	// need no workload deployment and therefore work unchanged on Cloud Foundry.
	gatewayServedRuntimes = []otypes.Runtime{
		otypes.RuntimeRemote,
		otypes.RuntimeVMCP,
	}

	// deployedRuntimes require Obot to start a workload on an orchestrator it
	// controls. Cloud Foundry gives the app no such orchestrator, so every one
	// of these must report ErrNotSupportedByBackend rather than failing some
	// other way.
	deployedRuntimes = []otypes.Runtime{
		otypes.RuntimeUVX,
		otypes.RuntimeNPX,
		otypes.RuntimeContainerized,
	}

	// The concrete type must satisfy the backend interface. If upstream adds a
	// method to that interface, this fails to compile -- which is the point.
	_ backend = (*cloudFoundryBackend)(nil)
)

// requireNotSupported asserts the error is an ErrNotSupportedByBackend naming
// this backend. The concrete type matters: the API layer type-asserts on it to
// return 404 instead of 500, so a plain error here would surface as a server
// error to the client.
func requireNotSupported(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected ErrNotSupportedByBackend, got nil")
	}

	var notSupported *ErrNotSupportedByBackend
	if !errors.As(err, &notSupported) {
		t.Fatalf("expected *ErrNotSupportedByBackend, got %T: %v", err, err)
	}
	if notSupported.Backend != RuntimeBackendCloudFoundry {
		t.Fatalf("expected backend %q, got %q", RuntimeBackendCloudFoundry, notSupported.Backend)
	}
}

func TestCloudFoundryRuntimeIsGatewayServed(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime)+" is gateway served", func(t *testing.T) {
			if !runtimeIsGatewayServed(runtime) {
				t.Fatalf("expected %q to be gateway served", runtime)
			}
		})
	}

	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime)+" is not gateway served", func(t *testing.T) {
			if runtimeIsGatewayServed(runtime) {
				t.Fatalf("expected %q to require deployment", runtime)
			}
		})
	}
}

func TestCloudFoundryEnsureServerDeploymentAllowsGatewayServedRuntimes(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime), func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			server := ServerConfig{Runtime: runtime, URL: "https://mcp.example.gov/mcp"}

			got, err := backend.ensureServerDeployment(t.Context(), server)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.URL != server.URL {
				t.Fatalf("URL = %q, want %q", got.URL, server.URL)
			}
		})
	}
}

func TestCloudFoundryEnsureServerDeploymentRejectsDeployedRuntimes(t *testing.T) {
	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime), func(t *testing.T) {
			backend := &cloudFoundryBackend{}

			if _, err := backend.ensureServerDeployment(t.Context(), ServerConfig{Runtime: runtime}); err == nil {
				t.Fatal("expected error for runtime requiring deployment")
			} else {
				requireNotSupported(t, err)
			}
		})
	}
}

// ensureServerDeployment rewrites component and webhook URLs before the runtime
// check, exactly as the docker backend does. transformObotHostname is the
// identity function here, so the URLs must survive untouched -- this pins that
// the rewrite loop does not drop or corrupt them.
func TestCloudFoundryEnsureServerDeploymentPreservesComponentAndWebhookURLs(t *testing.T) {
	backend := &cloudFoundryBackend{}
	server := ServerConfig{
		Runtime:    otypes.RuntimeVMCP,
		Components: []ComponentServer{{URL: "https://component.apps.internal/mcp"}},
		Webhooks:   []Webhook{{URL: "https://webhook.apps.internal/hook"}},
	}

	got, err := backend.ensureServerDeployment(t.Context(), server)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got.Components) != 1 || got.Components[0].URL != "https://component.apps.internal/mcp" {
		t.Fatalf("component URL not preserved: %+v", got.Components)
	}
	if len(got.Webhooks) != 1 || got.Webhooks[0].URL != "https://webhook.apps.internal/hook" {
		t.Fatalf("webhook URL not preserved: %+v", got.Webhooks)
	}
}

func TestCloudFoundryDeployServer(t *testing.T) {
	for _, runtime := range gatewayServedRuntimes {
		t.Run(string(runtime)+" succeeds", func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			if err := backend.deployServer(t.Context(), ServerConfig{Runtime: runtime}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	for _, runtime := range deployedRuntimes {
		t.Run(string(runtime)+" is unsupported", func(t *testing.T) {
			backend := &cloudFoundryBackend{}
			requireNotSupported(t, backend.deployServer(t.Context(), ServerConfig{Runtime: runtime}))
		})
	}
}

func TestCloudFoundryUnsupportedOperations(t *testing.T) {
	backend := &cloudFoundryBackend{}

	t.Run("getServerDetails", func(t *testing.T) {
		_, err := backend.getServerDetails(t.Context(), "any-server")
		requireNotSupported(t, err)
	})

	t.Run("streamServerLogs", func(t *testing.T) {
		readCloser, err := backend.streamServerLogs(t.Context(), "any-server")
		requireNotSupported(t, err)
		if readCloser != nil {
			t.Fatal("expected nil ReadCloser alongside the error")
		}
	})

	t.Run("restartServer", func(t *testing.T) {
		requireNotSupported(t, backend.restartServer(t.Context(), ServerConfig{Runtime: otypes.RuntimeRemote}))
	})
}

// shutdownServer must never error, for any runtime. It is called from delete and
// finalizer paths as well as the idle-disable path, and an error there wedges the
// finalizer instead of degrading. Nothing is ever deployed, so there is nothing
// to shut down. This is the single most load-bearing behavior in the backend.
func TestCloudFoundryShutdownServerNeverErrors(t *testing.T) {
	backend := &cloudFoundryBackend{}
	allRuntimes := append(append([]otypes.Runtime{}, gatewayServedRuntimes...), deployedRuntimes...)

	for _, runtime := range allRuntimes {
		for _, hardShutdown := range []bool{false, true} {
			name := string(runtime)
			if hardShutdown {
				name += " hard"
			}
			t.Run(name, func(t *testing.T) {
				if err := backend.shutdownServer(t.Context(), "any-server", hardShutdown); err != nil {
					t.Fatalf("shutdownServer must not error, got: %v", err)
				}
			})
		}
	}
}

// transformObotHostname is the identity function: Obot is reachable at its
// external Cloud Foundry route and no MCP workload runs anywhere needing a
// rewritten hostname. Routing over the CF internal domain would change this.
func TestCloudFoundryTransformObotHostnameIsIdentity(t *testing.T) {
	backend := &cloudFoundryBackend{}

	for _, url := range []string{
		"https://obot.app.cloud.gov/oauth/token",
		"http://localhost:8080/oauth/token",
		"https://cdc-places.apps.internal:8080/mcp",
		"not-a-url",
		"",
	} {
		if got := backend.transformObotHostname(url); got != url {
			t.Fatalf("transformObotHostname(%q) = %q, want identity", url, got)
		}
	}
}

// remoteConfig must not widen the operator's address-range flags. Only the
// allow list is the backend's contribution. Widening the flags here would
// relax blocking for EVERY remote server, including operator- and
// partner-supplied URLs, which is the hole this design exists to avoid.
func TestCloudFoundryRemoteConfigDoesNotWidenOperatorFlags(t *testing.T) {
	backend := &cloudFoundryBackend{}

	for _, global := range []RemoteMCPURLValidationConfig{
		{},
		{AllowLocalhostMCP: true, AllowPrivateIPMCP: true, AllowLinkLocalMCP: true},
		{AllowPrivateIPMCP: true},
		{AllowLocalhostMCP: true},
	} {
		got, _ := backend.remoteConfig(global)

		if got.AllowLocalhostMCP != global.AllowLocalhostMCP ||
			got.AllowPrivateIPMCP != global.AllowPrivateIPMCP ||
			got.AllowLinkLocalMCP != global.AllowLinkLocalMCP {
			t.Fatalf("remoteConfig(%+v) changed the operator flags: %+v", global, got)
		}
	}
}

// The allow list contains the standard internal listener and one exact DIU exception.
//
// MCP servers this backend cannot deploy are instead run as their own CF apps
// on apps.internal, which resolves into 10.255.0.0/16 -- RFC1918. Without this
// entry, Obot's default DisallowPrivateIPMCP rejects them at admission, before
// any network plumbing is exercised.
func TestCloudFoundryRemoteConfigAllowsInternalDomain(t *testing.T) {
	backend := &cloudFoundryBackend{}

	_, allowList := backend.remoteConfig(RemoteMCPURLValidationConfig{})

	require.Equal(t, []string{"*.apps.internal:8080", "mcp-diu-solutions.apps.internal:8001"}, allowList)
}

// The allow list is only useful if it admits real internal routes and nothing
// else. Assert against safehttp.HostAllowed -- the same function the dialer
// uses -- rather than re-deriving the matching rules here.
func TestCloudFoundryAllowListScope(t *testing.T) {
	backend := &cloudFoundryBackend{}
	_, allowList := backend.remoteConfig(RemoteMCPURLValidationConfig{})

	tests := []struct {
		name string
		host string
		port string
		want bool
	}{
		{name: "internal MCP route", host: "mcp-cdc-places.apps.internal", port: "8080", want: true},
		{name: "another internal route", host: "mcp-nih-reporter.apps.internal", port: "8080", want: true},
		{
			name: "DIU listener",
			host: "mcp-diu-solutions.apps.internal",
			port: "8001",
			want: true,
		},
		{
			name: "another internal host on DIU port",
			host: "mcp-cdc-places.apps.internal",
			port: "8001",
			want: false,
		},
		{
			name: "DIU on unapproved port",
			host: "mcp-diu-solutions.apps.internal",
			port: "8002",
			want: false,
		},
		{name: "internal route on another port", host: "mcp-cdc-places.apps.internal", port: "80", want: false},
		{name: "internal apex is not a route", host: "apps.internal", port: "8080", want: false},
		{name: "lookalike domain", host: "evil-apps.internal", port: "8080", want: false},
		{name: "attacker-controlled suffix", host: "apps.internal.evil.com", port: "443", want: false},
		{name: "public route", host: "mcp-server-hub.app.cloud.gov", port: "443", want: false},
		{name: "cloud metadata endpoint", host: "169.254.169.254", port: "80", want: false},
		{name: "loopback", host: "127.0.0.1", port: "8080", want: false},
		{name: "arbitrary private IP", host: "10.0.0.5", port: "8080", want: false},
		{name: "kubernetes service domain", host: "svc.cluster.local", port: "443", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safehttp.HostAllowed(allowList, tt.host, tt.port); got != tt.want {
				t.Errorf("HostAllowed(%q, %q) = %v, want %v", tt.host, tt.port, got, tt.want)
			}
		})
	}
}

// The compile-time interface assertion lives in the var block at the top of this
// file. This test covers the constructor instead: it must hand back a usable
// implementation rather than a typed nil, which would satisfy the interface at
// compile time but panic on first use.
func TestNewCloudFoundryBackendReturnsUsableBackend(t *testing.T) {
	b := newCloudFoundryBackend(true, 8080, Options{})
	if b == nil {
		t.Fatal("expected a backend implementation, got nil")
	}

	cf, ok := b.(*cloudFoundryBackend)
	if !ok {
		t.Fatalf("expected *cloudFoundryBackend, got %T", b)
	}
	if cf == nil {
		t.Fatal("expected a non-nil *cloudFoundryBackend")
	}
	if !cf.authEnabled {
		t.Error("authEnabled = false, want true (constructor argument ignored)")
	}
	if cf.httpListenPort != 8080 {
		t.Errorf("httpListenPort = %d, want 8080 (constructor argument ignored)", cf.httpListenPort)
	}
}

// Run tests that replace process-global DNS or leave asynchronous HTTP dials in
// an isolated copy of the existing test binary. A race-enabled binary remains
// race-enabled in the child; failures and output propagate to the parent.
func runMCPTestInSubprocess(t *testing.T) bool {
	t.Helper()
	const marker = "OBOT_MCP_ISOLATED_TEST"
	if os.Getenv(marker) == t.Name() {
		return false
	}
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run="+strings.Join(parts, "/"), "-test.v", "-test.count=1", "-test.timeout=45s")
	command.Env = append(os.Environ(), marker+"="+t.Name())
	output, err := command.CombinedOutput()
	t.Logf("isolated test output:\n%s", output)
	require.NoError(t, err, "isolated test failed")
	return true
}

// Controlled DNS proves range rejection, not merely failure to resolve. These
// tests are offline regressions, not live cloud.gov end-to-end verification.
// Call only in subprocess-isolated tests, never in the parent test process.
func useCloudFoundryPrivateDNS(t *testing.T) {
	t.Helper()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go answerCloudFoundryDNS(t, server)
			return client, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = previous })
}

func answerCloudFoundryDNS(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Error(err)
		return
	}
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		t.Error(err)
		return
	}
	query := make([]byte, binary.BigEndian.Uint16(length[:]))
	if _, err := io.ReadFull(conn, query); err != nil {
		t.Error(err)
		return
	}
	var message dnsmessage.Message
	if err := message.Unpack(query); err != nil {
		t.Error(err)
		return
	}
	message.Response = true
	message.RecursionAvailable = true
	for _, question := range message.Questions {
		if question.Type == dnsmessage.TypeA {
			message.Answers = append(message.Answers, dnsmessage.Resource{
				Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
				Body:   &dnsmessage.AResource{A: [4]byte{10, 255, 0, 10}},
			})
		}
	}
	response, err := message.Pack()
	if err != nil {
		t.Error(err)
		return
	}
	binary.BigEndian.PutUint16(length[:], uint16(len(response)))
	if _, err := conn.Write(append(length[:], response...)); err != nil {
		t.Error(err)
	}
}

func TestCloudFoundryInternalURLPassesRemoteValidation(t *testing.T) {
	if runMCPTestInSubprocess(t) {
		return
	}
	useCloudFoundryPrivateDNS(t)
	manager := &SessionManager{backend: &cloudFoundryBackend{}}
	options := manager.ValidationOptions()
	require.NoError(t, ValidateCatalogEntryManifest(t.Context(), cloudFoundryRemoteManifest("https://8.8.8.8/mcp"), true, options))
	for _, rawURL := range []string{
		"http://mcp-cdc-places.apps.internal:8080/mcp",
		"http://mcp-nih-reporter.apps.internal:8080/mcp",
		"http://mcp-diu-solutions.apps.internal:8001/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			require.NoError(t, ValidateRemoteMCPURL(t.Context(), rawURL, options.RemoteMCPURLValidationConfig))
			require.NoError(t, ValidateCatalogEntryManifest(t.Context(), cloudFoundryRemoteManifest(rawURL), true, options))
			got, err := manager.LaunchServer(t.Context(), ServerConfig{Runtime: otypes.RuntimeRemote, URL: rawURL, StartupTimeout: time.Second})
			require.NoError(t, err)
			require.Equal(t, rawURL, got.URL)
		})
	}
	for _, rawURL := range []string{
		"http://mcp-cdc-places.apps.internal:8001/mcp",
		"http://mcp-diu-solutions.apps.internal:8002/mcp",
		"http://mcp-cdc-places.apps.internal/mcp",
		"http://apps.internal:8080/mcp",
		"http://evil-apps.internal:8080/mcp",
		"http://mcp-diu-solutions.apps.internal.attacker.example:8001/mcp",
		"http://mcp-diu-solutions-apps.internal:8001/mcp",
		"http://10.0.0.5:8080/mcp",
		"http://[fc00::1]:8080/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			err := ValidateCatalogEntryManifest(t.Context(), cloudFoundryRemoteManifest(rawURL), true, options)
			require.ErrorContains(t, err, "private IP")
			var validationError otypes.RuntimeValidationError
			require.ErrorAs(t, err, &validationError)
			require.Equal(t, "fixedURL", validationError.Field)
			_, err = manager.LaunchServer(t.Context(), ServerConfig{Runtime: otypes.RuntimeRemote, URL: rawURL})
			require.ErrorContains(t, err, "private IP")
		})
	}
	require.ErrorContains(t, ValidateCatalogEntryManifest(t.Context(), cloudFoundryRemoteManifest(
		"http://mcp-cdc-places.apps.internal:8080/mcp"), true, ValidationOptions{}), "private IP")
}

func cloudFoundryRemoteManifest(rawURL string) otypes.MCPServerCatalogEntryManifest {
	return otypes.MCPServerCatalogEntryManifest{
		Name:             "Internal MCP",
		Description:      "Internal MCP",
		ShortDescription: "Internal MCP",
		Icon:             "icon",
		Runtime:          otypes.RuntimeRemote,
		RemoteConfig:     &otypes.RemoteCatalogConfig{FixedURL: rawURL},
	}
}

func TestCloudFoundryAdmissionBlocksLocalAndMetadataAddresses(t *testing.T) {
	options := (&SessionManager{backend: &cloudFoundryBackend{}}).ValidationOptions()
	for _, rawURL := range []string{
		"http://localhost:8080/mcp",
		"http://service.localhost:8080/mcp",
		"http://127.0.0.1:8080/mcp",
		"http://[::1]:8080/mcp",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/mcp",
		"http://[fd00:ec2::254]/mcp",
		"http://100.100.100.200/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			require.Error(t, ValidateCatalogEntryManifest(t.Context(), cloudFoundryRemoteManifest(rawURL), true, options))
			require.Error(t, safehttp.ValidateURL(t.Context(), rawURL, options.RemoteMCPURLValidationConfig.SafeHTTPOptions()))
		})
	}
	// The metadata classification belongs to link-local policy, independently
	// of the private-IP flag, just as in the outbound transport.
	config := options.RemoteMCPURLValidationConfig
	config.AllowPrivateIPMCP = true
	require.Error(t, ValidateRemoteMCPURL(t.Context(), "http://[fd00:ec2::254]/mcp", config))
	require.ErrorContains(t, ValidateRemoteMCPURL(t.Context(), "http://100.100.100.200/mcp", config), "link-local")
	config.AllowLinkLocalMCP = true
	require.NoError(t, ValidateRemoteMCPURL(t.Context(), "http://100.100.100.200/mcp", config))
}
