package ambient

type Form string

const (
	FormReference Form = "reference"
	FormCall      Form = "call"
	FormConstruct Form = "construct"
)

type KeyPolicy string

const (
	KeyNone   KeyPolicy = "none"
	KeyMember KeyPolicy = "member"
)

type ModuleBinding struct {
	Specifier string
	Export    string
	Path      []string
}

type Matcher struct {
	Capability string
	Root       string
	Path       []string
	Modules    []ModuleBinding
	Form       Form
	ArgCount   *int
	Key        KeyPolicy
}

type Source struct {
	ID          string
	Description string
	Matchers    []Matcher
}

func Builtin() []Source {
	zero := 0
	perfHooks := []ModuleBinding{
		{Specifier: "node:perf_hooks", Export: "performance", Path: []string{}},
		{Specifier: "perf_hooks", Export: "performance", Path: []string{}},
	}
	processModules := []ModuleBinding{
		{Specifier: "node:process", Export: "default", Path: []string{}},
		{Specifier: "node:process", Export: "env", Path: []string{"env"}},
		{Specifier: "process", Export: "default", Path: []string{}},
		{Specifier: "process", Export: "env", Path: []string{"env"}},
	}
	processMember := func(name string) []ModuleBinding {
		return []ModuleBinding{
			{Specifier: "node:process", Export: "default", Path: []string{}},
			{Specifier: "node:process", Export: name, Path: []string{name}},
			{Specifier: "process", Export: "default", Path: []string{}},
			{Specifier: "process", Export: name, Path: []string{name}},
		}
	}
	cryptoMember := func(name string) []ModuleBinding {
		return []ModuleBinding{
			{Specifier: "node:crypto", Export: "default", Path: []string{}},
			{Specifier: "node:crypto", Export: name, Path: []string{name}},
			{Specifier: "crypto", Export: "default", Path: []string{}},
			{Specifier: "crypto", Export: name, Path: []string{name}},
		}
	}
	timerModules := func(name string) []ModuleBinding {
		return []ModuleBinding{
			{Specifier: "node:timers", Export: name, Path: []string{}},
			{Specifier: "timers", Export: name, Path: []string{}},
		}
	}
	cryptoRef := func(capability, name string) Matcher {
		return Matcher{Capability: capability, Root: "crypto", Path: []string{name}, Modules: cryptoMember(name), Form: FormReference, Key: KeyNone}
	}
	timerRef := func(name string) Matcher {
		return Matcher{Capability: "schedule", Root: name, Path: []string{}, Modules: timerModules(name), Form: FormReference, Key: KeyNone}
	}
	return []Source{
		{
			ID:          "clock",
			Description: "Reads the current time.",
			Matchers: []Matcher{
				{Capability: "now", Root: "Date", Path: []string{"now"}, Form: FormReference, Key: KeyNone},
				{Capability: "now", Root: "performance", Path: []string{"now"}, Modules: perfHooks, Form: FormReference, Key: KeyNone},
				{Capability: "construct_now", Root: "Date", Path: []string{}, Form: FormConstruct, ArgCount: &zero, Key: KeyNone},
				{Capability: "construct_now", Root: "Date", Path: []string{}, Form: FormCall, Key: KeyNone},
			},
		},
		{
			ID:          "env",
			Description: "Reads or writes process environment variables.",
			Matchers: []Matcher{
				{Capability: "read", Root: "process", Path: []string{"env"}, Modules: processModules, Form: FormReference, Key: KeyMember},
			},
		},
		{
			ID:          "random",
			Description: "Reads a random value.",
			Matchers: []Matcher{
				{Capability: "number", Root: "Math", Path: []string{"random"}, Form: FormReference, Key: KeyNone},
				cryptoRef("number", "randomInt"),
				cryptoRef("uuid", "randomUUID"),
				cryptoRef("bytes", "getRandomValues"),
				cryptoRef("bytes", "randomBytes"),
				cryptoRef("bytes", "randomFillSync"),
			},
		},
		{
			ID:          "crypto",
			Description: "Uses the Web Crypto API, which generates keys and random values.",
			Matchers: []Matcher{
				cryptoRef("subtle", "subtle"),
			},
		},
		{
			ID:          "network",
			Description: "Sends a network request.",
			Matchers: []Matcher{
				{Capability: "fetch", Root: "fetch", Path: []string{}, Form: FormReference, Key: KeyNone},
			},
		},
		{
			ID:          "timer",
			Description: "Schedules a callback for later.",
			Matchers: []Matcher{
				timerRef("setTimeout"),
				timerRef("setInterval"),
				timerRef("setImmediate"),
				{Capability: "schedule", Root: "queueMicrotask", Path: []string{}, Form: FormReference, Key: KeyNone},
			},
		},
		{
			ID:          "process",
			Description: "Reads process state other than the environment.",
			Matchers: []Matcher{
				{Capability: "cwd", Root: "process", Path: []string{"cwd"}, Modules: processMember("cwd"), Form: FormReference, Key: KeyNone},
				{Capability: "argv", Root: "process", Path: []string{"argv"}, Modules: processMember("argv"), Form: FormReference, Key: KeyNone},
			},
		},
	}
}
