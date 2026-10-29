export function launder(read: () => number): number {
	return read() + 1;
}
