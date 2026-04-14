export type Predicate<T = any> = (value: T) => boolean;

type QueryKind = "functions" | "imports" | "calls" | "typeRefs" | "accesses";

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

export type AccessView = {
	root: string;
	accessPath: string;
	origin: "special_form";
	filePath: string;
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

type BaseQuery = {
	__lintaiKind: "query";
	entity: QueryKind;
	ops: QueryOperation[];
	isEmpty(): AssertionValue;
};

export type FunctionsQuery = BaseQuery & {
	entity: "functions";
	in(pattern: string): FunctionsQuery;
	where(predicate: Predicate<FunctionView>): FunctionsQuery;
	calling(other: FunctionsQuery): FunctionsQuery;
	transitivelyCalling(other: FunctionsQuery): FunctionsQuery;
};

export type ImportsQuery = BaseQuery & {
	entity: "imports";
	from(pattern: string): ImportsQuery;
	to(pattern: string): ImportsQuery;
	where(predicate: Predicate<ImportEdgeView>): ImportsQuery;
};

export type CallsQuery = BaseQuery & {
	entity: "calls";
	from(pattern: string): CallsQuery;
	to(pattern: string): CallsQuery;
	where(predicate: Predicate<CallEdgeView>): CallsQuery;
};

export type TypeRefsQuery = BaseQuery & {
	entity: "typeRefs";
	in(pattern: string): TypeRefsQuery;
	where(predicate: Predicate<TypeRefView>): TypeRefsQuery;
};

export type AccessesQuery = BaseQuery & {
	entity: "accesses";
	in(pattern: string): AccessesQuery;
	where(predicate: Predicate<AccessView>): AccessesQuery;
};

export type QueryValue = FunctionsQuery | ImportsQuery | CallsQuery | TypeRefsQuery | AccessesQuery;

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

function createBaseQuery(entity: QueryKind, ops: QueryOperation[]): BaseQuery {
	return {
		__lintaiKind: "query",
		entity,
		ops,
		isEmpty() {
			return {
				__lintaiKind: "assertion",
				terminal: "isEmpty",
				query: this as QueryValue,
			};
		},
	};
}

function createFunctionsQuery(ops: QueryOperation[] = []): FunctionsQuery {
	return {
		...createBaseQuery("functions", ops),
		entity: "functions",
		in(pattern: string) {
			return createFunctionsQuery([...ops, { type: "in", value: pattern }]);
		},
		where(predicate: Predicate<FunctionView>) {
			return createFunctionsQuery([...ops, { type: "where", handler: predicate }]);
		},
		calling(other: FunctionsQuery) {
			return createFunctionsQuery([...ops, { type: "calling", query: other }]);
		},
		transitivelyCalling(other: FunctionsQuery) {
			return createFunctionsQuery([...ops, { type: "transitivelyCalling", query: other }]);
		},
	};
}

function createImportsQuery(ops: QueryOperation[] = []): ImportsQuery {
	return {
		...createBaseQuery("imports", ops),
		entity: "imports",
		from(pattern: string) {
			return createImportsQuery([...ops, { type: "from", value: pattern }]);
		},
		to(pattern: string) {
			return createImportsQuery([...ops, { type: "to", value: pattern }]);
		},
		where(predicate: Predicate<ImportEdgeView>) {
			return createImportsQuery([...ops, { type: "where", handler: predicate }]);
		},
	};
}

function createCallsQuery(ops: QueryOperation[] = []): CallsQuery {
	return {
		...createBaseQuery("calls", ops),
		entity: "calls",
		from(pattern: string) {
			return createCallsQuery([...ops, { type: "from", value: pattern }]);
		},
		to(pattern: string) {
			return createCallsQuery([...ops, { type: "to", value: pattern }]);
		},
		where(predicate: Predicate<CallEdgeView>) {
			return createCallsQuery([...ops, { type: "where", handler: predicate }]);
		},
	};
}

function createTypeRefsQuery(ops: QueryOperation[] = []): TypeRefsQuery {
	return {
		...createBaseQuery("typeRefs", ops),
		entity: "typeRefs",
		in(pattern: string) {
			return createTypeRefsQuery([...ops, { type: "in", value: pattern }]);
		},
		where(predicate: Predicate<TypeRefView>) {
			return createTypeRefsQuery([...ops, { type: "where", handler: predicate }]);
		},
	};
}

function createAccessesQuery(ops: QueryOperation[] = []): AccessesQuery {
	return {
		...createBaseQuery("accesses", ops),
		entity: "accesses",
		in(pattern: string) {
			return createAccessesQuery([...ops, { type: "in", value: pattern }]);
		},
		where(predicate: Predicate<AccessView>) {
			return createAccessesQuery([...ops, { type: "where", handler: predicate }]);
		},
	};
}

export function rule<Env = Record<string, any>, Setup = Record<string, never>, Value = any>(
	id: string,
): RuleDefinition<Env, Setup, Value> {
	return new RuleDefinition<Env, Setup, Value>({ id });
}

export function functions(): FunctionsQuery {
	return createFunctionsQuery();
}

export function imports(): ImportsQuery {
	return createImportsQuery();
}

export function calls(): CallsQuery {
	return createCallsQuery();
}

export function typeRefs(): TypeRefsQuery {
	return createTypeRefsQuery();
}

export function accesses(): AccessesQuery {
	return createAccessesQuery();
}
