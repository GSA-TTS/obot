package mcpcatalog

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/obot-platform/nah/pkg/router"
	"github.com/obot-platform/obot/pkg/mcp"
	v1 "github.com/obot-platform/obot/pkg/storage/apis/obot.obot.ai/v1"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

func newCloudFoundryCatalogHandler(t *testing.T) *Handler {
	t.Helper()
	manager, err := mcp.NewSessionManager(t.Context(), true, false, nil, nil,
		"https://obot.example.gov", 8080, mcp.Options{
			MCPRuntimeBackend:    mcp.RuntimeBackendCloudFoundry,
			DisallowLocalhostMCP: true,
			DisallowPrivateIPMCP: true,
			DisallowLinkLocalMCP: true,
		}, nil, nil, nil, nil, nil, nil, "default", nil)
	require.NoError(t, err)
	handler := newParseTestHandler(t)
	return New("", "", handler.gatewayClient, nil, manager, 0)
}

func writeCloudFoundryCatalogEntry(t *testing.T, dir, rawURL string) {
	t.Helper()
	manifest := fmt.Sprintf(`type: entry
entryKey: internal-component
name: Internal Component
shortDescription: Internal component
description: Internal component
icon: icon
runtime: remote
remoteConfig:
  fixedURL: %s
`, rawURL)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "component.yaml"), []byte(manifest), 0o600))
}

// Like the MCP policy tests, use private DNS answers, not DNS failures, as the
// negative oracle. No real cloud.gov DNS or application connections are used.
// These tests replace DefaultResolver only in isolated test subprocesses.
func runCatalogTestInSubprocess(t *testing.T) bool {
	t.Helper()
	const marker = "OBOT_CATALOG_ISOLATED_TEST"
	if os.Getenv(marker) == t.Name() {
		return false
	}
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1", "-test.timeout=45s")
	command.Env = append(os.Environ(), marker+"="+t.Name())
	output, err := command.CombinedOutput()
	t.Logf("isolated test output:\n%s", output)
	require.NoError(t, err, "isolated test failed")
	return true
}

func useCatalogPrivateDNS(t *testing.T) {
	t.Helper()
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go answerCatalogPrivateDNS(t, server)
			return client, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = previous })
}

func answerCatalogPrivateDNS(t *testing.T, conn net.Conn) {
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

func TestCloudFoundryCatalogAdmissionUsesSessionManagerPolicy(t *testing.T) {
	if runCatalogTestInSubprocess(t) {
		return
	}
	useCatalogPrivateDNS(t)
	handler := newCloudFoundryCatalogHandler(t)
	for _, rawURL := range []string{
		"http://mcp-cdc-places.apps.internal:8080/mcp",
		"http://mcp-nih-reporter.apps.internal:8080/mcp",
		"http://mcp-diu-solutions.apps.internal:8001/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			dir := t.TempDir()
			writeCloudFoundryCatalogEntry(t, dir, rawURL)
			objects, _, err := handler.readMCPCatalog(t.Context(), "default", dir, "")
			require.NoError(t, err)
			require.Len(t, objects, 1)
			entry, ok := objects[0].(*v1.MCPServerCatalogEntry)
			require.True(t, ok)
			require.Equal(t, rawURL, entry.Spec.Manifest.RemoteConfig.FixedURL)
		})
	}
	for _, rawURL := range []string{
		"http://mcp-cdc-places.apps.internal:8001/mcp",
		"http://mcp-diu-solutions.apps.internal:8002/mcp",
		"http://mcp-cdc-places.apps.internal/mcp",
		"http://apps.internal:8080/mcp",
		"http://evil-apps.internal:8080/mcp",
		"http://mcp-diu-solutions.apps.internal.attacker.example:8001/mcp",
		"http://10.0.0.5:8080/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			dir := t.TempDir()
			writeCloudFoundryCatalogEntry(t, dir, rawURL)
			objects, _, err := handler.readMCPCatalog(t.Context(), "default", dir, "")
			require.Empty(t, objects)
			require.ErrorContains(t, err, "private IP")
			require.ErrorContains(t, err, "fixedURL")
		})
	}
}

func TestCloudFoundryCatalogSyncResolvesPrivateComponent(t *testing.T) {
	if runCatalogTestInSubprocess(t) {
		return
	}
	useCatalogPrivateDNS(t)
	dir := t.TempDir()
	writeCloudFoundryCatalogEntry(t, dir, "http://mcp-diu-solutions.apps.internal:8001/mcp")
	vmcp := `type: vmcp
entryKey: diu-product
displayName: DIU Product
components:
  - id: diu
    name: Internal Component
    mcpServerCatalogEntryKey: internal-component
profiles:
  - name: everyone
    subjects:
      - type: selector
        id: "*"
    vmcpPermissions:
      allowedComponents:
        diu:
          allowedTools:
            - search_solutions
            - get_solution
            - list_solutions
            - catalog_facets
            - search
            - fetch
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vmcp.yaml"), []byte(vmcp), 0o600))
	catalog := testCatalog()
	catalog.Spec.SourceURLs = []string{dir}
	client := newCatalogFakeClient(catalog)
	handler := newCloudFoundryCatalogHandler(t)
	require.NoError(t, handler.Sync(router.Request{Ctx: t.Context(), Client: client, Object: catalog}, &parseTestResponse{}))
	require.NoError(t, client.Get(t.Context(), router.Key(catalog.Namespace, catalog.Name), catalog))
	require.Empty(t, catalog.Status.SyncErrors)
	var entries v1.MCPServerCatalogEntryList
	require.NoError(t, client.List(t.Context(), &entries))
	require.Len(t, entries.Items, 1)
	var vmcps v1.VMCPList
	require.NoError(t, client.List(t.Context(), &vmcps))
	require.Len(t, vmcps.Items, 1)
	require.Equal(t, entries.Items[0].Name, vmcps.Items[0].Spec.Manifest.Components[0].MCPServerCatalogEntryID)
	permissions := vmcps.Items[0].Spec.Manifest.Profiles[0].Permissions
	require.False(t, permissions.AllowAllComponents)
	require.ElementsMatch(t, []string{"search_solutions", "get_solution", "list_solutions", "catalog_facets", "search", "fetch"}, permissions.AllowedComponents["diu"].AllowedTools)
	require.NotContains(t, permissions.AllowedComponents["diu"].AllowedTools, "catalog_status")
}

func TestCloudFoundryCatalogBlocksLocalAndMetadataAddresses(t *testing.T) {
	handler := newCloudFoundryCatalogHandler(t)
	for _, rawURL := range []string{
		"http://localhost:8080/mcp",
		"http://127.0.0.1:8080/mcp",
		"http://[::1]:8080/mcp",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/mcp",
		"http://100.100.100.200/mcp",
		"http://[fd00:ec2::254]/mcp",
	} {
		t.Run(rawURL, func(t *testing.T) {
			dir := t.TempDir()
			writeCloudFoundryCatalogEntry(t, dir, rawURL)
			objects, _, err := handler.readMCPCatalog(t.Context(), "default", dir, "")
			require.Empty(t, objects)
			require.ErrorContains(t, err, "fixedURL")
			require.NotContains(t, err.Error(), "failed to resolve")
		})
	}
}
