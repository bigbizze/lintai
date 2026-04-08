import { fetchData } from "../services/fetchData";

export function loadValue() {
	return fetchData();
}
