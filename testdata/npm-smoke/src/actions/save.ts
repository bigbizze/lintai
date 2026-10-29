import type { ActionResult } from "../core/result";

export async function save(): Promise<ActionResult<number>> {
	return { ok: true, value: 1 };
}

export async function legacySave(): Promise<number> {
	return 1;
}
