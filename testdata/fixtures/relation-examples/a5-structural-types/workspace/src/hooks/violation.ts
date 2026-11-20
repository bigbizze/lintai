export function optionalEvent(callback: (event?: string) => void): void { void callback; }
export function manyEvents(callback: (...events: string[]) => void): void { void callback; }
export function returningCallback(callback: (event: string) => number): void { void callback; }
export function missingEvent(callback: () => void): void { void callback; }
