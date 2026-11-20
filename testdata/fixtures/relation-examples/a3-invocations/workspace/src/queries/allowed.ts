interface Store {
  read(): void;
}

function write(): void {}
function double(value: number): number { return value * 2; }

export function inspect(store: Store): void {
  store.read();
  write();
  [1].map(double);
}
