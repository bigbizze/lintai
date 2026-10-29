import { lookup } from "catalog-api";

export function handler(id: string) {
	return lookup(id);
}
