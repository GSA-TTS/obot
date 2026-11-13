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

function setup() {
	const inner = {
		send: vi.fn().mockResolvedValue(undefined),
		close: vi.fn().mockResolvedValue(undefined)
	} as unknown as StreamableHTTPClientTransport;
	const sink: MCPMessageSink = {
		recordMessage: vi.fn(),
		recordLifecycle: vi.fn(),
		recordError: vi.fn()
	};
	const transport = new LoggingTransport(inner, sink);
	const receive = (message: JSONRPCMessage) => inner.onmessage?.(message);
	return { transport, receive, sink };
}

describe('LoggingTransport response capture', () => {
	it('rejects oversized responses before capture, logging, or SDK forwarding', async () => {
		const { transport, receive, sink } = setup();
		const capture = transport.captureResponse('tools/call');
		transport.onmessage = vi.fn();
		await transport.send({ jsonrpc: '2.0', id: 1, method: 'tools/call' });
		receive({
			jsonrpc: '2.0',
			id: 1,
			result: { values: Array(MCP_MESSAGE_LIMITS.containerEntries + 1).fill(null) }
		});
		expect(capture.response).toBeUndefined();
		expect(transport.onmessage).not.toHaveBeenCalled();
		expect(sink.recordMessage).not.toHaveBeenCalledWith('incoming', expect.anything());
		expect(transport.failure?.limit).toBe('containerEntries');
	});

	it('matches the outgoing method and typed id, before forwarding the response to the SDK', async () => {
		const { transport, receive } = setup();
		const capture = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 1, method: 'tools/list' });
		await transport.send({ jsonrpc: '2.0', id: 2, method: 'tools/call' });
		receive({ jsonrpc: '2.0', id: 1, result: {} });
		receive({ jsonrpc: '2.0', id: '2', result: {} });
		receive({ jsonrpc: '2.0', id: 2, method: 'sampling/createMessage', params: {} });
		receive({ jsonrpc: '2.0', method: 'notifications/message', params: {} });
		await transport.send({ jsonrpc: '2.0', id: 2, result: {} });
		expect(capture.response).toBeUndefined();
		const response = { jsonrpc: '2.0' as const, id: 2, result: { content: [] } };
		transport.onmessage = () => expect(capture.response).toEqual(response);
		receive(response);
		expect(capture.response).toEqual(response);
	});

	it('keeps interleaved responses with their own requests and retains error envelopes', async () => {
		const { transport, receive } = setup();
		const first = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 1, method: 'tools/call' });
		const second = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 2, method: 'tools/call' });
		const error = {
			jsonrpc: '2.0' as const,
			id: 2,
			error: { code: -32602, message: 'Invalid arguments', data: { field: 'query' } }
		};
		receive(error);
		expect(first.response).toBeUndefined();
		expect(second.response).toEqual(error);
		receive({ jsonrpc: '2.0', id: 1, result: { content: [] } });
		expect(first.response?.id).toBe(1);
		expect(second.response).toEqual(error);
	});

	it('does not let a disposed capture claim a later request or response', async () => {
		const { transport, receive } = setup();
		const unsent = transport.captureResponse('tools/call');
		unsent.dispose();
		const cancelled = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 1, method: 'tools/call' });
		cancelled.dispose();
		const next = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 2, method: 'tools/call' });
		receive({ jsonrpc: '2.0', id: 1, result: {} });
		expect(next.response).toBeUndefined();
		receive({ jsonrpc: '2.0', id: 2, result: {} });
		expect(unsent.response).toBeUndefined();
		expect(cancelled.response).toBeUndefined();
		expect(next.response?.id).toBe(2);
	});

	it.each(['detach', 'close'] as const)('discards pending captures on %s', async (action) => {
		const { transport, receive } = setup();
		const capture = transport.captureResponse('tools/call');
		await transport.send({ jsonrpc: '2.0', id: 1, method: 'tools/call' });
		await transport[action]();
		receive({ jsonrpc: '2.0', id: 1, result: {} });
		expect(capture.response).toBeUndefined();
	});
});
