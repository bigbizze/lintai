async function localWork() {
	return 1;
}

export function calculate(fetch: (url: string) => number) {
	return fetch("/local") + 1;
}

export function account() {
	return localWork();
}
