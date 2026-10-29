export type ActionResult<T> = { ok: true; value: T } | { ok: false; error: string };
