import "server-only";
import { type Config, port } from "./config";

export function handle(name: string, config: Config) {
	return [port, config.port, process.env.SECRET, process.env[name]];
}
