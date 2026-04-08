package query

import "github.com/dop251/goja"

type Plan struct {
	Entity string
	Ops    []Operation
}

type Operation struct {
	Type    string
	Value   string
	Query   *Plan
	Handler goja.Value
}

type Assertion struct {
	AssertionID string
	Terminal    string
	Query       *Plan
}
