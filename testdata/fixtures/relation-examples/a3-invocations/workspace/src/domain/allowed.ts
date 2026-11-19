export function age(created: number, clock: { now(): number }): number {
  return clock.now() - created;
}
