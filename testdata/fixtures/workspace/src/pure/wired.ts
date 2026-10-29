import { launder } from "./launder";

export function wired(source: () => number): number {
	return launder(source);
}
