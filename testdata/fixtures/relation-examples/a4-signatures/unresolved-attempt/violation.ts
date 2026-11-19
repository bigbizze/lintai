import type { Missing } from "./missing";
export function missing(value: Missing): Missing { return value; }
export function unavailable(value: UnknownType): UnknownType { return value; }
export class Example {
  constructor(value: Missing) { void value; }
  get value(): Missing { throw new Error(); }
  set value(value: Missing) { void value; }
}
