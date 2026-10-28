export type Fragment = {
	readonly __lintaiKind: "fragment";
	readonly text: string;
};

export type Name = {
	readonly __lintaiKind: "name";
	readonly value: string;
};

export type DlValue = string | number | Name | Fragment;

export type FactValue = string | number | Name;

export type Facts = Record<string, FactValue[][]>;

export type RowValue = string | number;

export type Row = Record<string, RowValue>;

export type MessageContext = {
	assertion_id: string;
};

export type RuleContext<Env, Setup> = {
	env: Env;
	setup: Setup;
};

export type SetupContext<Env> = {
	env: Env;
	workspaceRoot: string;
};

const namePattern = /^[a-z][a-z0-9_]*(\/[a-z0-9_]+)*$/;

function isTagged(value: unknown, kind: "fragment" | "name"): boolean {
	return (
		typeof value === "object" &&
		value !== null &&
		(value as { __lintaiKind?: unknown }).__lintaiKind === kind
	);
}

function describe(value: unknown): string {
	if (value === null) return "null";
	if (Array.isArray(value)) return "an array";
	return typeof value;
}

function hex(code: number, width: number): string {
	return code.toString(16).padStart(width, "0");
}

function escapeString(value: string): string {
	let result = '"';
	for (const char of value) {
		const code = char.codePointAt(0) ?? 0;
		switch (char) {
			case "\\":
				result += "\\\\";
				break;
			case '"':
				result += '\\"';
				break;
			case "'":
				result += "\\'";
				break;
			case "\n":
				result += "\\n";
				break;
			case "\t":
				result += "\\t";
				break;
			default:
				if (code < 0x20 || code === 0x7f) {
					result += `\\x${hex(code, 2)}`;
				} else if (code > 0x7e) {
					result += `\\u{${hex(code, 4)}}`;
				} else {
					result += char;
				}
		}
	}
	return `${result}"`;
}

function formatNumber(value: number): string {
	if (!Number.isFinite(value)) {
		throw new TypeError(`dl: cannot interpolate non-finite number ${value}`);
	}
	if (Number.isInteger(value)) {
		if (!Number.isSafeInteger(value)) {
			throw new TypeError(`dl: integer ${value} is outside the safe integer range`);
		}
		return String(value);
	}
	const text = String(value);
	if (text.includes("e")) {
		throw new TypeError(`dl: cannot interpolate ${value}; use a number without an exponent`);
	}
	return text;
}

function formatValue(value: unknown): string {
	if (typeof value === "string") return escapeString(value);
	if (typeof value === "number") return formatNumber(value);
	if (isTagged(value, "fragment")) return (value as Fragment).text;
	if (isTagged(value, "name")) return `/${(value as Name).value}`;
	throw new TypeError(
		`dl: cannot interpolate ${describe(value)}; use a string, a finite number, dl.name() or a dl fragment`,
	);
}

function name(value: string): Name {
	if (typeof value !== "string" || !namePattern.test(value)) {
		throw new TypeError(
			`dl.name: ${JSON.stringify(value)} is not a valid name; it must match ${namePattern}`,
		);
	}
	return Object.freeze({ __lintaiKind: "name", value });
}

function dlTag(strings: TemplateStringsArray, ...values: DlValue[]): Fragment {
	let text = strings[0] ?? "";
	for (let index = 0; index < values.length; index++) {
		text += formatValue(values[index]);
		text += strings[index + 1] ?? "";
	}
	return Object.freeze({ __lintaiKind: "fragment", text });
}

export type Dl = typeof dlTag & { readonly name: typeof name };

export const dl: Dl = Object.defineProperty(
	(strings: TemplateStringsArray, ...values: DlValue[]) => dlTag(strings, ...values),
	"name",
	{ value: name, enumerable: true },
) as Dl;

export type ProgramInput<Env, Setup> = Fragment | ((ctx: RuleContext<Env, Setup>) => Fragment);

type RuleSpec<Env, Setup> = {
	id: string;
	versionValue?: number;
	setupFn?: (ctx: SetupContext<Env>) => Setup | Promise<Setup>;
	factsFn?: (ctx: RuleContext<Env, Setup>) => Facts;
	programValue?: ProgramInput<Env, Setup>;
	reportPredicates?: readonly string[];
	expectNonEmptyPredicates?: readonly string[];
	messageFn?: (row: Row, ctx: MessageContext) => string;
};

export class RuleDefinition<Env, Setup> {
	readonly id: string;
	readonly versionValue?: number;
	readonly setupFn?: RuleSpec<Env, Setup>["setupFn"];
	readonly factsFn?: RuleSpec<Env, Setup>["factsFn"];
	readonly programValue?: RuleSpec<Env, Setup>["programValue"];
	readonly reportPredicates?: readonly string[];
	readonly expectNonEmptyPredicates?: readonly string[];
	readonly messageFn?: RuleSpec<Env, Setup>["messageFn"];

	constructor(private readonly spec: RuleSpec<Env, Setup>) {
		this.id = spec.id;
		this.versionValue = spec.versionValue;
		this.setupFn = spec.setupFn;
		this.factsFn = spec.factsFn;
		this.programValue = spec.programValue;
		this.reportPredicates = spec.reportPredicates;
		this.expectNonEmptyPredicates = spec.expectNonEmptyPredicates;
		this.messageFn = spec.messageFn;
	}

	version(versionValue: number): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, versionValue });
	}

	setup<NextSetup>(
		setupFn: (ctx: SetupContext<Env>) => NextSetup | Promise<NextSetup>,
	): RuleDefinition<Env, Awaited<NextSetup>> {
		return new RuleDefinition<Env, Awaited<NextSetup>>({
			...(this.spec as unknown as RuleSpec<Env, Awaited<NextSetup>>),
			setupFn: setupFn as RuleSpec<Env, Awaited<NextSetup>>["setupFn"],
		});
	}

	facts(factsFn: (ctx: RuleContext<Env, Setup>) => Facts): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, factsFn });
	}

	program(programValue: ProgramInput<Env, Setup>): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, programValue });
	}

	report(...predicates: string[]): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, reportPredicates: predicates });
	}

	expectNonEmpty(...predicates: string[]): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, expectNonEmptyPredicates: predicates });
	}

	message(messageFn: (row: Row, ctx: MessageContext) => string): RuleDefinition<Env, Setup> {
		return new RuleDefinition({ ...this.spec, messageFn });
	}
}

export function rule<Env = Record<string, any>, Setup = Record<string, never>>(
	id: string,
): RuleDefinition<Env, Setup> {
	return new RuleDefinition<Env, Setup>({ id });
}
