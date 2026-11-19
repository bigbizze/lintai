class Promise<T> { value!: T; }
export function lookup(): Promise<string> { return new Promise<string>(); }
