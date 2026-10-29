// Content-Length framing for JSON messages, the same framing that lintai
// serve uses.
export function encodeFrame(message: unknown): Buffer {
	const payload = Buffer.from(JSON.stringify(message), "utf8");
	return Buffer.concat([Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`, "utf8"), payload]);
}

export class FrameDecoder {
	private buffer = Buffer.alloc(0);

	push(chunk: Buffer): unknown[] {
		this.buffer = Buffer.concat([this.buffer, chunk]);
		const messages: unknown[] = [];
		while (true) {
			const headerEnd = this.buffer.indexOf("\r\n\r\n");
			if (headerEnd < 0) {
				return messages;
			}
			const match = /^Content-Length:\s*(\d+)$/im.exec(this.buffer.subarray(0, headerEnd).toString("utf8"));
			if (!match) {
				throw new Error("invalid frame header");
			}
			const payloadEnd = headerEnd + 4 + Number.parseInt(match[1], 10);
			if (this.buffer.length < payloadEnd) {
				return messages;
			}
			messages.push(JSON.parse(this.buffer.subarray(headerEnd + 4, payloadEnd).toString("utf8")));
			this.buffer = this.buffer.subarray(payloadEnd);
		}
	}
}
