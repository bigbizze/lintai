type Callback = (value: string) => void;
export function invoke(callback: Callback) { callback("ok"); }
