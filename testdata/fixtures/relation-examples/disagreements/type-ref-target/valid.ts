import type { Model as DirectModel } from "./model.js";
import type { PublicModel as ThroughChain } from "./chain.js";
export function direct(value: DirectModel) {
	return value;
}
export function chained(value: ThroughChain) {
	return value;
}
