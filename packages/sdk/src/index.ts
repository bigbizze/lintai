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

/** Kinds in `suspension_site`. Pass these strings to `dl.name`. */
export type SuspensionKind = "await" | "for_await" | "await_using";

/** Reasons in `site_attribution_unsupported`. Pass these strings to `dl.name`. */
export type SiteAttributionUnsupportedReason = "instance_construction" | "unsupported_context";

export type RowValue = string | number;

export type Row = Record<string, RowValue>;

export type SuppressionPolicy = {
	readonly by?: readonly string[];
};

export type SuppressionDeclaration = {
	readonly assertion: string;
	readonly options: SuppressionPolicy;
};

export type MessageContext = {
	assertion_id: string;
};

export type JsonValue =
	| null
	| boolean
	| number
	| string
	| readonly JsonValue[]
	| { readonly [key: string]: JsonValue };

/** Optional named config export. The engine supplies the rule's options object. */
export type RuleConfig<Env extends JsonValue = JsonValue> = {
	parse(value: Readonly<Record<string, JsonValue>>): Env;
};

type DeepReadonly<T> = T extends object ? { readonly [K in keyof T]: DeepReadonly<T[K]> } : T;

export type RuleContext<Env> = {
	readonly env: DeepReadonly<Env>;
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

export type ProgramInput<Env> = Fragment | ((ctx: RuleContext<Env>) => Fragment);

type RuleSpec<Env> = {
	id: string;
	versionValue?: number;
	factsFn?: (ctx: RuleContext<Env>) => DeepReadonly<Facts>;
	programValue?: ProgramInput<Env>;
	reportPredicates?: readonly string[];
	expectNonEmptyPredicates?: readonly string[];
	suppressionDeclarations?: readonly SuppressionDeclaration[];
	messageFn?: (row: Row, ctx: MessageContext) => string;
};

export class RuleDefinition<Env> {
	readonly id: string;
	readonly versionValue?: number;
	readonly factsFn?: RuleSpec<Env>["factsFn"];
	readonly programValue?: RuleSpec<Env>["programValue"];
	readonly reportPredicates?: readonly string[];
	readonly expectNonEmptyPredicates?: readonly string[];
	readonly suppressionDeclarations?: readonly SuppressionDeclaration[];
	readonly messageFn?: RuleSpec<Env>["messageFn"];

	constructor(private readonly spec: RuleSpec<Env>) {
		this.id = spec.id;
		this.versionValue = spec.versionValue;
		this.factsFn = spec.factsFn;
		this.programValue = spec.programValue;
		this.reportPredicates = spec.reportPredicates;
		this.expectNonEmptyPredicates = spec.expectNonEmptyPredicates;
		this.suppressionDeclarations = spec.suppressionDeclarations;
		this.messageFn = spec.messageFn;
	}

	version(versionValue: number): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, versionValue });
	}

	facts(factsFn: (ctx: RuleContext<Env>) => DeepReadonly<Facts>): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, factsFn });
	}

	program(programValue: ProgramInput<Env>): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, programValue });
	}

	report(...predicates: string[]): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, reportPredicates: predicates });
	}

	expectNonEmpty(...predicates: string[]): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, expectNonEmptyPredicates: predicates });
	}

	suppression(assertion: string, options: SuppressionPolicy = {}): RuleDefinition<Env> {
		// Preserve invalid fields and values for the engine's program analysis.
		const policy =
			options !== null && typeof options === "object" && !Array.isArray(options)
				? (Object.freeze({
						...Object.fromEntries(
							Object.keys(options)
								.filter((key) => key !== "by")
								.map((key) => [key, null]),
						),
						...(options.by === undefined
							? {}
							: {
									by: Array.isArray(options.by)
										? Object.freeze(
												options.by.map((value) => (typeof value === "string" ? value : null)),
											)
										: null,
								}),
					}) as SuppressionPolicy)
				: options;
		const declaration: SuppressionDeclaration = { assertion, options: policy };
		return new RuleDefinition({
			...this.spec,
			suppressionDeclarations: Object.freeze([
				...(this.spec.suppressionDeclarations ?? []),
				declaration,
			]),
		});
	}

	message(messageFn: (row: Row, ctx: MessageContext) => string): RuleDefinition<Env> {
		return new RuleDefinition({ ...this.spec, messageFn });
	}
}

export const ruleIdPattern = /^[a-z0-9][a-z0-9._/-]*$/;

export function rule<Env = Record<string, JsonValue>>(id: string): RuleDefinition<Env> {
	if (typeof id !== "string" || !ruleIdPattern.test(id)) {
		throw new TypeError(
			`rule id ${JSON.stringify(id)} is not valid: it must match ${ruleIdPattern}`,
		);
	}
	return new RuleDefinition<Env>({ id });
}
