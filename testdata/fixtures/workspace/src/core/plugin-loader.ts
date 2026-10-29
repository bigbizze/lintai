export async function loadPlugin(name: string): Promise<unknown> {
	const specifier = `./plugins/${name}`;
	return import(specifier);
}
