import type { InfrastructureRecord } from "../infrastructure/record";

export function send(record: InfrastructureRecord) {
	return record.id;
}
