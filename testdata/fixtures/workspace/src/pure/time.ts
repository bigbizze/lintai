export function stamp(items: string[]) {
	return items.map(() => Date.now());
}
