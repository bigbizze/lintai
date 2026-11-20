export function parse(value: string): string;
export function parse(value: number): number;
export function parse(value: string | number): string | number { return value; }
export const check = (value: string | undefined) => value;
export class Widget { method() { return 1; } }
