// Content-Length counts UTF-8 bytes. Accumulate the payload in chunks so a
// multi-megabyte response is copied only once, when it is complete.
export function encodeFrame(message: unknown): Buffer {
	const payload = Buffer.from(JSON.stringify(message));
	return Buffer.concat([Buffer.from(`Content-Length: ${payload.length}\r\n\r\n`), payload]);
}

export class FrameDecoder {
	private header = Buffer.alloc(0);
	private remaining = 0;
	private parts: Buffer[] = [];

	push(bytes: Buffer): unknown[] {
		let chunk = bytes;
		const messages: unknown[] = [];
		while (chunk.length) {
			if (!this.remaining) {
				if (this.header.length) chunk = Buffer.concat([this.header, chunk]);
				const end = chunk.indexOf("\r\n\r\n");
				if (end > 8192 || (end < 0 && chunk.length > 8192)) throw new Error("invalid frame header");
				if (end < 0) {
					this.header = Buffer.from(chunk);
					break;
				}
				const match = /^Content-Length:[ \t]*(\d+)$/i.exec(chunk.toString("ascii", 0, end));
				if (!match) throw new Error("invalid frame header");
				this.remaining = Number(match[1]);
				if (!Number.isSafeInteger(this.remaining) || !this.remaining)
					throw new Error("invalid frame length");
				this.header = Buffer.alloc(0);
				chunk = chunk.subarray(end + 4);
			}
			const take = Math.min(this.remaining, chunk.length);
			this.parts.push(chunk.subarray(0, take));
			chunk = chunk.subarray(take);
			this.remaining -= take;
			if (!this.remaining) {
				messages.push(JSON.parse(Buffer.concat(this.parts).toString("utf8")));
				this.parts = [];
			}
		}
		return messages;
	}
}
