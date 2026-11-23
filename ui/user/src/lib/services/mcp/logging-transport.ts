import { m } from '$lib/i18n';
import { assertMCPMessageLimits, MCPMessageLimitError } from './message-limits';
import type { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import type {
	Transport,
	TransportSendOptions
} from '@modelcontextprotocol/sdk/shared/transport.js';
import {
	isJSONRPCErrorResponse,
	isJSONRPCRequest,
	isJSONRPCResultResponse,
	type JSONRPCMessage,
	type JSONRPCErrorResponse,
	type JSONRPCResultResponse,
	type MessageExtraInfo,
	type RequestId
} from '@modelcontextprotocol/sdk/types.js';

export type MCPRawResponse = JSONRPCResultResponse | JSONRPCErrorResponse;

interface ResponseCapture {
	method: string;
	requestId?: RequestId;
	response?: MCPRawResponse;
	dispose(): void;
}

export type MCPMessageDirection = 'outgoing' | 'incoming';

export interface MCPMessageSink {
	recordMessage(direction: MCPMessageDirection, message: JSONRPCMessage): void;
	recordLifecycle(event: 'connecting' | 'closed'): void;
	recordError(error: Error): void;
}

export class LoggingTransport implements Transport {
	readonly inner: StreamableHTTPClientTransport;
	#sink: MCPMessageSink | undefined;
	#failure: MCPMessageLimitError | undefined;
	#closed = false;
	#captures = new Set<ResponseCapture>();

	get failure(): MCPMessageLimitError | undefined {
		return this.#failure;
	}

	onclose?: () => void;
	onerror?: (error: Error) => void;
	onmessage?: (message: JSONRPCMessage, extra?: MessageExtraInfo) => void;

	constructor(inner: StreamableHTTPClientTransport, sink: MCPMessageSink) {
		this.inner = inner;
		this.#sink = sink;
		inner.onmessage = ((message: JSONRPCMessage, extra?: MessageExtraInfo) => {
			this.#receive(message, extra);
		}) as StreamableHTTPClientTransport['onmessage'];
		inner.onerror = (error) => {
			this.#sink?.recordError(error);
			this.onerror?.(error);
		};
		inner.onclose = () => {
			this.#notifyClosed();
		};
	}

	#receive(message: JSONRPCMessage, extra?: MessageExtraInfo): void {
		if (this.#failure || this.#closed) return;
		try {
			assertMCPMessageLimits(message);
		} catch (error) {
			if (!(error instanceof MCPMessageLimitError)) throw error;
			this.#failure = new MCPMessageLimitError(error.limit, m.mcps_tester_message_limit_exceeded());
			this.#sink?.recordError(this.#failure);
			this.onerror?.(this.#failure);
			// Settle SDK requests immediately even if the underlying close later fails.
			// Closing also aborts streams and prevents automatic SSE reconnection.
			this.#notifyClosed();
			void this.inner.close().catch((closeError: unknown) => {
				const failure = closeError instanceof Error ? closeError : new Error(String(closeError));
				this.#sink?.recordError(failure);
				this.onerror?.(failure);
			});
			return;
		}
		if (isJSONRPCResultResponse(message) || isJSONRPCErrorResponse(message)) {
			for (const capture of this.#captures) {
				if (capture.requestId === message.id) {
					capture.response = message;
					capture.dispose();
				}
			}
		}
		this.#sink?.recordMessage('incoming', message);
		this.onmessage?.(message, extra);
	}

	#notifyClosed(): void {
		if (this.#closed) return;
		this.#closed = true;
		this.#captures.clear();
		this.#sink?.recordLifecycle('closed');
		this.onclose?.();
	}

	detach(): void {
		this.#sink = undefined;
		this.#captures.clear();
	}

	// Register immediately before invoking the SDK. The tester serializes tool calls;
	// bind to the outgoing request id rather than guessing from the latest log entry.
	captureResponse(method: string): ResponseCapture {
		const capture: ResponseCapture = {
			method,
			dispose: () => this.#captures.delete(capture)
		};
		this.#captures.add(capture);
		return capture;
	}

	async start(): Promise<void> {
		this.#sink?.recordLifecycle('connecting');
		await this.inner.start();
	}

	async send(message: JSONRPCMessage, options?: TransportSendOptions): Promise<void> {
		if (this.#failure) throw this.#failure;
		if (isJSONRPCRequest(message)) {
			for (const capture of this.#captures) {
				if (capture.requestId === undefined && capture.method === message.method) {
					capture.requestId = message.id;
					break;
				}
			}
		}
		this.#sink?.recordMessage('outgoing', message);
		await this.inner.send(message, options);
	}

	close(): Promise<void> {
		this.#captures.clear();
		return this.inner.close();
	}

	get sessionId(): string | undefined {
		return this.inner.sessionId;
	}

	setProtocolVersion(version: string): void {
		this.inner.setProtocolVersion(version);
	}
}
