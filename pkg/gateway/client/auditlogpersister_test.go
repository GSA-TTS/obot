package client

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	types2 "github.com/obot-platform/obot/apiclient/types"
	"github.com/obot-platform/obot/pkg/gateway/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
)

var (
	errTestAuditEncryption = errors.New("test audit encryption failure")
)

type failAfterAuditTransformer struct {
	calls     int
	failAfter int
}

func (t *failAfterAuditTransformer) TransformToStorage(_ context.Context, data []byte, _ value.Context) ([]byte, error) {
	t.calls++
	if t.calls > t.failAfter {
		return nil, errTestAuditEncryption
	}
	return append([]byte("encrypted:"), data...), nil
}

func (*failAfterAuditTransformer) TransformFromStorage(_ context.Context, data []byte, _ value.Context) ([]byte, bool, error) {
	return data, false, nil
}

func TestEncryptMCPAuditLogDoesNotMarkPartialEncryptionComplete(t *testing.T) {
	transformer := &failAfterAuditTransformer{failAfter: 1}
	c := newTestClient(t)
	c.encryptionConfig = auditEncryptionConfig(transformer)
	entry := testMCPAuditEntry()

	err := c.encryptMCPAuditLog(t.Context(), &entry)
	if !errors.Is(err, errTestAuditEncryption) {
		t.Fatalf("expected encryption failure, got %v", err)
	}
	if entry.Encrypted {
		t.Fatal("partially encrypted audit log was marked encrypted")
	}
}

func TestLogMCPAuditEntryDropsEntryAfterEncryptionFailure(t *testing.T) {
	transformer := &failAfterAuditTransformer{failAfter: 1}
	c := newTestClient(t)
	c.encryptionConfig = auditEncryptionConfig(transformer)
	c.auditBuffer = make([]types.MCPAuditLog, 0, 2)
	c.kickAuditPersist = make(chan struct{}, 1)

	c.LogMCPAuditEntry(testMCPAuditEntry())

	if len(c.auditBuffer) != 0 {
		t.Fatalf("expected failed audit entry to be dropped, buffered %d entries", len(c.auditBuffer))
	}
	if err := c.persistMCPAuditLogs(); err != nil {
		t.Fatalf("persist audit logs: %v", err)
	}
	if got := countAuditLogs(t, c); got != 0 {
		t.Fatalf("expected no persisted audit logs, got %d", got)
	}
}

func TestLogMCPAuditEntryQueuesEncryptedEntry(t *testing.T) {
	c := newTestClient(t)
	c.encryptionConfig = testEncryptionConfig()
	c.auditBuffer = make([]types.MCPAuditLog, 0, 2)
	c.kickAuditPersist = make(chan struct{}, 1)

	c.LogMCPAuditEntry(testMCPAuditEntry())

	if len(c.auditBuffer) != 1 {
		t.Fatalf("expected one buffered audit entry, got %d", len(c.auditBuffer))
	}
	if !c.auditBuffer[0].Encrypted {
		t.Fatal("expected buffered audit entry to be encrypted")
	}
	if err := c.persistMCPAuditLogs(); err != nil {
		t.Fatalf("persist audit logs: %v", err)
	}
	if got := countAuditLogs(t, c); got != 1 {
		t.Fatalf("expected one persisted audit log, got %d", got)
	}
}

func auditEncryptionConfig(transformer value.Transformer) *encryptionconfig.EncryptionConfiguration {
	return &encryptionconfig.EncryptionConfiguration{
		Transformers: map[schema.GroupResource]value.Transformer{
			mcpAuditLogGroupResource: transformer,
		},
	}
}

func testMCPAuditEntry() types.MCPAuditLog {
	return types.MCPAuditLog{
		CreatedAt:  time.Now().UTC(),
		SourceType: types2.AuditLogSourceTypeMCP,
		UserID:     "user-1",
		MCPFields: &types.MCPAuditLogFields{
			MCPID:        "mcp-1",
			CallType:     "tools/call",
			RequestBody:  json.RawMessage(`{"secret":"request"}`),
			ResponseBody: json.RawMessage(`{"secret":"response"}`),
		},
	}
}
