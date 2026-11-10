import { LoggingTransport, type MCPMessageSink } from './logging-transport';
import { MCP_MESSAGE_LIMITS } from './message-limits';
import { Client } from '@modelcontextprotocol/sdk/client/index.js';
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import { ListPromptsResultSchema, type JSONRPCMessage } from '@modelcontextprotocol/sdk/types.js';
import { describe, expect, it, vi } from 'vitest';

function sink(): MCPMessageSink {
	return { recordMessage: vi.fn(), recordLifecycle: vi.fn(), recordError: vi.fn() };
}

describe('LoggingTransport message rejection', () => {
	it.each(['json', 'sse'])(
		'closes %s requests before logging or result parsing',
		async (format) => {
			const log = sink();
			const parsed = vi.fn((result) => result);
			const inner = new StreamableHTTPClientTransport(new URL('https://example.com/mcp'), {
				fetch: async (_input, init) => {
					if (init?.method === 'GET') return new Response(null, { status: 405 });
					const request = JSON.parse(String(init?.body));
					if (request.method === 'notifications/initialized') {
						return new Response(null, { status: 202 });
					}
					const message = {
						jsonrpc: '2.0',
						id: request.id,
						result:
							request.method === 'initialize'
								? {
										protocolVersion: request.params.protocolVersion,
										capabilities: { prompts: {} },
										serverInfo: { name: 'test', version: '1' }
									}
								: { prompts: Array(MCP_MESSAGE_LIMITS.containerEntries + 1).fill({ name: 'test' }) }
					};
					return format === 'sse'
						? new Response(`data: ${JSON.stringify(message)}\n\n`, {
								headers: { 'Content-Type': 'text/event-stream' }
							})
						: Response.json(message);
				}
			});
			const close = vi.spyOn(inner, 'close');
			const transport = new LoggingTransport(inner, log);
			const client = new Client({ name: 'test', version: '1' }, { capabilities: {} });
			try {
				await client.connect(transport);
				await expect(
					client.request({ method: 'prompts/list' }, ListPromptsResultSchema.transform(parsed))
				).rejects.toMatchObject({ code: -32000 });
				expect(close).toHaveBeenCalledOnce();
				expect(parsed).not.toHaveBeenCalled();
				expect(log.recordMessage).not.toHaveBeenCalledWith(
					'incoming',
					expect.objectContaining({
						result: expect.objectContaining({ prompts: expect.any(Array) })
					})
				);
				expect(log.recordError).toHaveBeenCalledWith(
					expect.objectContaining({ code: 'MCP_MESSAGE_LIMIT_EXCEEDED' })
				);
				expect(transport.failure?.limit).toBe('containerEntries');
				await expect(transport.send({ jsonrpc: '2.0', method: 'ping' })).rejects.toBe(
					transport.failure
				);
			} finally {
				await client.close();
			}
		}
	);

	it('passes acceptable messages and extra metadata unchanged', () => {
		const inner = new StreamableHTTPClientTransport(new URL('https://example.com/mcp'));
		const log = sink();
		const transport = new LoggingTransport(inner, log);
		transport.onmessage = vi.fn();
		const message: JSONRPCMessage = { jsonrpc: '2.0', id: 1, result: { content: [] } };
		const extra = {};
		// LoggingTransport installs a metadata-capable callback; the concrete SDK
		// transport declares only the message argument on its public property.
		const receive: LoggingTransport['onmessage'] = inner.onmessage;
		receive?.(message, extra);
		expect(log.recordMessage).toHaveBeenCalledWith('incoming', message);
		expect(transport.onmessage).toHaveBeenCalledWith(message, extra);
		expect(transport.failure).toBeUndefined();
	});

	it('reports close failures and ignores subsequent frames after rejection', async () => {
		const inner = new StreamableHTTPClientTransport(new URL('https://example.com/mcp'));
		const log = sink();
		const error = new Error('close failed');
		vi.spyOn(inner, 'close').mockRejectedValue(error);
		const transport = new LoggingTransport(inner, log);
		transport.onerror = vi.fn();
		transport.onclose = vi.fn();
		transport.onmessage = vi.fn();
		inner.onmessage?.({ jsonrpc: '2.0', id: 1, result: { values: Array(1001).fill(null) } });
		inner.onmessage?.({ jsonrpc: '2.0', id: 2, result: {} });
		await vi.waitFor(() => expect(transport.onerror).toHaveBeenCalledWith(error));
		expect(transport.onclose).toHaveBeenCalledOnce();
		expect(log.recordMessage).not.toHaveBeenCalled();
		expect(transport.onmessage).not.toHaveBeenCalled();
	});
});
