export async function loadAccount() {
	return fetch("/accounts");
}
