interface Store {
  write(): void;
}

export function update(store: Store): void {
  store.write();
}
