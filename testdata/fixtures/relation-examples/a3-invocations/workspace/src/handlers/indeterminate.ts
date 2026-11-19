export function run(callback: (value: number) => boolean): boolean[] {
  return [1].map(callback);
}
