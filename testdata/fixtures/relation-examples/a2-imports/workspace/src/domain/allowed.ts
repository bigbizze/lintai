import type { DomainRecord } from "./model";

export function send(record: DomainRecord) {
	return record.id;
}
