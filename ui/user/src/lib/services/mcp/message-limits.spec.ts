import { assertMCPMessageLimits, MCP_MESSAGE_LIMITS, MCPMessageLimitError } from './message-limits';
import { describe, expect, it } from 'vitest';

function nested(depth: number): unknown {
	let value: unknown = null;
	for (let index = 0; index < depth; index++) value = { child: value };
	return value;
}

function nodeBudget(extra: number): unknown {
	// Root + 50 arrays + 49 * 999 leaves + 998 leaves = 50,000 nodes.
	return Array.from({ length: 50 }, (_, index) =>
		Array(index === 49 ? 998 + extra : 999).fill(null)
	);
}

function expectLimit(value: unknown, limit: keyof typeof MCP_MESSAGE_LIMITS): void {
	try {
		assertMCPMessageLimits(value);
		throw new Error('Expected limit rejection');
	} catch (error) {
		expect(error).toBeInstanceOf(MCPMessageLimitError);
		expect(error).toMatchObject({ code: 'MCP_MESSAGE_LIMIT_EXCEEDED', limit });
	}
}

describe('decoded MCP message limits', () => {
	it('accepts exact container limits and rejects one more entry', () => {
		const entries = MCP_MESSAGE_LIMITS.containerEntries;
		expect(() => assertMCPMessageLimits(Array(entries).fill(null))).not.toThrow();
		expectLimit(Array(entries + 1).fill(null), 'containerEntries');
		const object = Object.fromEntries(Array.from({ length: entries }, (_, i) => [`key${i}`, i]));
		expect(() => assertMCPMessageLimits(object)).not.toThrow();
		expectLimit({ ...object, extra: null }, 'containerEntries');
	});

	it('counts the root and every value against the aggregate node limit', () => {
		expect(() => assertMCPMessageLimits(nodeBudget(0))).not.toThrow();
		expectLimit(nodeBudget(1), 'nodes');
	});

	it('accepts depth 64 with the root at zero and rejects depth 65', () => {
		expect(() => assertMCPMessageLimits(nested(MCP_MESSAGE_LIMITS.depth))).not.toThrow();
		expectLimit(nested(MCP_MESSAGE_LIMITS.depth + 1), 'depth');
	});

	it('checks nested arrays in schema, metadata and error data', () => {
		const oversized = Array(MCP_MESSAGE_LIMITS.containerEntries + 1).fill(0);
		for (const value of [
			{ result: { tools: [{ inputSchema: { required: oversized } }] } },
			{ params: { _meta: { nested: oversized } } },
			{ error: { data: { nested: oversized } } }
		]) {
			expectLimit(value, 'containerEntries');
		}
	});

	it('preserves large strings and deeply nested JSON represented as text', () => {
		const text = `${'['.repeat(800)}0${']'.repeat(800)}`;
		const message = {
			result: { content: [{ type: 'text', text }], large: 'x'.repeat(300 * 1024) }
		};
		expect(() => assertMCPMessageLimits(message)).not.toThrow();
		expect(message.result.content[0].text).toBe(text);
	});

	it('does not allocate an unbounded traversal for sparse arrays or cycles', () => {
		expectLimit(Array(MCP_MESSAGE_LIMITS.containerEntries + 1), 'containerEntries');
		const cyclic: { child?: unknown } = {};
		cyclic.child = cyclic;
		expectLimit(cyclic, 'depth');
	});
});
