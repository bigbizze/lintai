export interface Reader {
	read(): void;
}
export interface Writer {
	write(): void;
}
export type { Writer as PublicWriter };
export interface Other {
	write(): void;
}
export interface Capabilities {
	store: Reader & Writer;
}

export type SharedWrite = () => void;
export interface SharedWriter {
	write: SharedWrite;
}
export interface TextWriter {
	write(value: string): void;
}
export interface NumberWriter {
	write(value: number): void;
}
