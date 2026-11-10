import { fetchData } from "../services/fetch-data";

export function loadValue() {
	return fetchData();
}
