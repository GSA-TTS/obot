import { m } from '$lib/i18n';
import { assertMCPMessageLimits, MCPMessageLimitError } from './message-limits';
import type { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js';
import type {
	Transport,
	TransportSendOptions
} from '@modelcontextprotocol/sdk/shared/transport.js';
import type { JSONRPCMessage, MessageExtraInfo } from '@modelcontextprotocol/sdk/types.js';

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
		this.#sink?.recordMessage('incoming', message);
		this.onmessage?.(message, extra);
	}

	#notifyClosed(): void {
		if (this.#closed) return;
		this.#closed = true;
		this.#sink?.recordLifecycle('closed');
		this.onclose?.();
	}

	detach(): void {
		this.#sink = undefined;
	}

	async start(): Promise<void> {
		this.#sink?.recordLifecycle('connecting');
		await this.inner.start();
	}

	async send(message: JSONRPCMessage, options?: TransportSendOptions): Promise<void> {
		if (this.#failure) throw this.#failure;
		this.#sink?.recordMessage('outgoing', message);
		await this.inner.send(message, options);
	}

	close(): Promise<void> {
		return this.inner.close();
	}

	get sessionId(): string | undefined {
		return this.inner.sessionId;
	}

	setProtocolVersion(version: string): void {
		this.inner.setProtocolVersion(version);
	}
}
