import type {
	Capabilities, NumberWriter, Other, SharedWriter, TextWriter,
} from "./contracts";

export function complete(cap: Capabilities, other: Other) {
	cap.store.read();
	cap.store.write();
	const store = cap.store;
	store.write();
	other.write();
}

export function uncertain(shared: SharedWriter, union: TextWriter | NumberWriter, dynamic: any) {
	shared.write();
	union.write(undefined as never);
	dynamic();
}

function local() {}
local();
Date.now();
