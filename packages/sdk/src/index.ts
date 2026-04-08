export type Predicate<T = any> = (value: T) => boolean;

type QueryKind = "functions" | "imports" | "calls" | "typeRefs";

type QueryOperation =
	| { type: "in"; value: string }
	| { type: "from"; value: string }
	| { type: "to"; value: string }
	| { type: "where"; handler: Predicate<any> }
	| { type: "calling"; query: QueryValue }
	| { type: "transitivelyCalling"; query: QueryValue };

export type FunctionView = {
	name: string;
	kind: string;
	filePath: string;
	containerName: string;
	semanticKey: string;
	containsAwait: boolean;
	isExported: boolean;
	isAsync: boolean;
	parameterCount: number;
	returnTypeText: string;
	parameterTypeTexts: string[];
	sourceLocation: SourceLocation;
};

export type ImportedSymbolView = {
	name: string;
	kind: "default" | "namespace" | "named";
	isTypeOnly: boolean;
};

export type ImportEdgeView = {
	specifier: string;
	fromPath: string;
	toPath: string;
	semanticKey: string;
	importedSymbols: ImportedSymbolView[];
	hasDefaultImport: boolean;
	hasNamespaceImport: boolean;
	hasNamedImports: boolean;
	isTypeOnly: boolean;
	sourceLocation: SourceLocation;
};

export type CallEdgeView = {
	fromName: string;
	toName: string;
	fromPath: string;
	toPath: string;
	semanticKey: string;
	sourceLocation: SourceLocation;
};

export type TypeRefView = {
	name: string;
	filePath: string;
	targetPath: string;
	semanticKey: string;
	sourceLocation: SourceLocation;
};

export type SourceLocation = {
	file: string;
	startLine: number;
	startColumn: number;
	endLine: number;
	endColumn: number;
};

export type AssertionContext = {
	assertion_id: string;
};

export type RuleContext<Env, Setup> = {
	env: Env;
	setup: Setup;
};

export type AssertionValue = {
	__lintaiKind: "assertion";
	terminal: "isEmpty";
	query: QueryValue;
};

export type QueryValue = {
	__lintaiKind: "query";
	entity: QueryKind;
	ops: QueryOperation[];
	in(pattern: string): QueryValue;
	from(pattern: string): QueryValue;
	to(pattern: string): QueryValue;
	where(predicate: Predicate<any>): QueryValue;
	calling(other: QueryValue): QueryValue;
	transitivelyCalling(other: QueryValue): QueryValue;
	isEmpty(): AssertionValue;
};

type InternalRuleSpec<Env, Setup, Value> = {
	id: string;
	versionValue?: number;
	setupFn?: (() => Setup) | ((ctx: { env: Env; workspaceRoot: string }) => Setup);
	assertFn?: (ctx: RuleContext<Env, Setup>) => AssertionValue | Record<string, AssertionValue>;
	messageFn?: (value: Value, ctx: AssertionContext) => string;
};

class RuleDefinition<Env, Setup, Value> {
	readonly id: string;
	readonly versionValue?: number;
	readonly setupFn?: InternalRuleSpec<Env, Setup, Value>["setupFn"];
	readonly assertFn?: InternalRuleSpec<Env, Setup, Value>["assertFn"];
	readonly messageFn?: InternalRuleSpec<Env, Setup, Value>["messageFn"];

	constructor(private readonly spec: InternalRuleSpec<Env, Setup, Value>) {
		this.id = spec.id;
		this.versionValue = spec.versionValue;
		this.setupFn = spec.setupFn;
		this.assertFn = spec.assertFn;
		this.messageFn = spec.messageFn;
	}

	version(versionValue: number): RuleDefinition<Env, Setup, Value> {
		return new RuleDefinition({ ...this.spec, versionValue });
	}

	setup<NextSetup>(
		setupFn: InternalRuleSpec<Env, NextSetup, Value>["setupFn"],
	): RuleDefinition<Env, NextSetup, Value> {
		return new RuleDefinition<Env, NextSetup, Value>({
			...(this.spec as unknown as InternalRuleSpec<Env, NextSetup, Value>),
			setupFn,
		});
	}

	assert(
		assertFn: InternalRuleSpec<Env, Setup, Value>["assertFn"],
	): RuleDefinition<Env, Setup, Value> {
		return new RuleDefinition({
			...this.spec,
			assertFn,
		});
	}

	message(
		messageFn: InternalRuleSpec<Env, Setup, Value>["messageFn"],
	): RuleDefinition<Env, Setup, Value> {
		return new RuleDefinition({
			...this.spec,
			messageFn,
		});
	}
}

function createQuery(entity: QueryKind, ops: QueryOperation[] = []): QueryValue {
	const query: QueryValue = {
		__lintaiKind: "query",
		entity,
		ops,
		in(pattern: string) {
			return createQuery(entity, [...ops, { type: "in", value: pattern }]);
		},
		from(pattern: string) {
			return createQuery(entity, [...ops, { type: "from", value: pattern }]);
		},
		to(pattern: string) {
			return createQuery(entity, [...ops, { type: "to", value: pattern }]);
		},
		where(predicate: Predicate<any>) {
			return createQuery(entity, [...ops, { type: "where", handler: predicate }]);
		},
		calling(other: QueryValue) {
			return createQuery(entity, [...ops, { type: "calling", query: other }]);
		},
		transitivelyCalling(other: QueryValue) {
			return createQuery(entity, [
				...ops,
				{ type: "transitivelyCalling", query: other },
			]);
		},
		isEmpty() {
			return {
				__lintaiKind: "assertion",
				terminal: "isEmpty",
				query,
			};
		},
	};
	return query;
}

export function rule<Env = Record<string, any>, Setup = Record<string, never>, Value = any>(
	id: string,
): RuleDefinition<Env, Setup, Value> {
	return new RuleDefinition<Env, Setup, Value>({ id });
}

export function functions(): QueryValue {
	return createQuery("functions");
}

export function imports(): QueryValue {
	return createQuery("imports");
}

export function calls(): QueryValue {
	return createQuery("calls");
}

export function typeRefs(): QueryValue {
	return createQuery("typeRefs");
}
