export const MCP_MESSAGE_LIMITS = {
	containerEntries: 1_000,
	nodes: 50_000,
	depth: 64
} as const;

export class MCPMessageLimitError extends Error {
	readonly code = 'MCP_MESSAGE_LIMIT_EXCEEDED';

	constructor(
		readonly limit: keyof typeof MCP_MESSAGE_LIMITS,
		message = 'MCP message exceeds the decoded structure limit'
	) {
		super(message);
		this.name = 'MCPMessageLimitError';
	}
}

// Only inspect decoded JSON values, never parse strings containing JSON. Count children
// before queuing them so neither traversal nor its bookkeeping can grow without a bound.
export function assertMCPMessageLimits(message: unknown): void {
	const pending = [{ value: message, depth: 0 }];
	let nodes = 1;
	while (pending.length) {
		const { value, depth } = pending.pop()!;
		if (depth > MCP_MESSAGE_LIMITS.depth) throw new MCPMessageLimitError('depth');
		if (value === null || typeof value !== 'object') continue;
		if (Array.isArray(value) && value.length > MCP_MESSAGE_LIMITS.containerEntries) {
			throw new MCPMessageLimitError('containerEntries');
		}
		let entries = 0;
		for (const key in value) {
			if (!Object.hasOwn(value, key)) continue;
			if (++entries > MCP_MESSAGE_LIMITS.containerEntries) {
				throw new MCPMessageLimitError('containerEntries');
			}
			if (++nodes > MCP_MESSAGE_LIMITS.nodes) throw new MCPMessageLimitError('nodes');
			pending.push({ value: (value as Record<string, unknown>)[key], depth: depth + 1 });
		}
	}
}
