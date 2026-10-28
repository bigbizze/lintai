export type Config = { port: string | undefined };

export const port = process.env.PORT;

export function mode(): string {
	return import.meta.env.MODE;
}
