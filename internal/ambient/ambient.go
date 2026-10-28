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
	}
}
