export function optionalPoint(point: readonly [number, number?]): void { void point; }
export function variablePoint(point: readonly [number, ...number[]]): void { void point; }
export function mutablePoint(point: [number, number]): void { void point; }
