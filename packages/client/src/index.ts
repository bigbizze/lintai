export { startSpawner } from "./spawner.js";

export {
	LintAIClient,
	createFramedResponse,
	createTestDependencies,
	createTestProcess,
	getClient,
	loadRuleIds,
	resetClientsForTest,
	resolveBridgeOptions,
	type Diagnostic,
	type HostDefaults,
	type LintaiOptions,
	type ResolvedBridgeOptions,
	type RuleIdList,
	type SetupSandbox,
} from "./client.js";

export {
	hasUnsavedChanges,
	programLocation,
	toReports,
	unsavedChangesMessage,
	type Report,
} from "./reports.js";

export {
	analysisRuleName,
	authoringRuleNames,
	createLintaiRules,
	reservedRuleNames,
	type LintaiRules,
	type LintedFile,
} from "./plugin.js";
