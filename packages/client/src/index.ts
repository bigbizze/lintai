export { startSpawner } from "./spawner.js";

export {
	LintAIClient,
	getClient,
	loadRuleIds,
	resolveBridgeOptions,
	type Diagnostic,
	type FileAnalysis,
	type FilesAnalysis,
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
	type BatchOperation,
	type LintaiRules,
	type LintedFile,
} from "./plugin.js";
