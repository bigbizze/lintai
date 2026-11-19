interface Secret { secret: string }
export function greet(value: Secret): string { return value.secret; }
export function unsafe(value: { payload: any }): string { return String(value.payload); }
