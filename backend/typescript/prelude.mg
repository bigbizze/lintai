# Derived TypeScript relations. Reporting support comes from base dependencies.
Decl site_in_fn(Site, Fn).
site_in_fn(Site, Fn) :- site_owner(Site, Fn), fn(Fn, _, _).
Decl site_at_module(Site, Module).
site_at_module(Site, Module) :- site_owner(Site, Module), module(Module).
Decl site_attribution_unknown(Site).
site_attribution_unknown(Site) :- site_attribution_unsupported(Site, _).
Decl fn_awaits(Fn).
fn_awaits(Fn) :- suspension_site(Site, /await), site_owner(Site, Fn), fn(Fn, _, _).
Decl fn_suspends(Fn).
fn_suspends(Fn) :- suspension_site(Site, _), site_owner(Site, Fn), fn(Fn, _, _).
Decl module_has_suspension(File).
module_has_suspension(File) :- site(Site, File), suspension_site(Site, _).
Decl import_has_symbols(Imp).
import_has_symbols(Imp) :- import_symbol(Imp, _, _).
Decl import_runtime(Imp).
import_runtime(Imp) :- import_edge(Imp, _, _, _), !import_type_only(Imp), !import_has_symbols(Imp).
import_runtime(Imp) :- import_retained(Imp).
import_runtime(Imp) :- import_symbol(Imp, Name, _), !import_type_only(Imp), !import_symbol_type_only(Imp, Name).
Decl module_imports(From, To).
module_imports(From, To) :- import_runtime(Imp), import_edge(Imp, From, To, _).
Decl imports_reach(From, To).
imports_reach(From, To) :- module_imports(From, To).
imports_reach(From, To) :- module_imports(From, Mid), imports_reach(Mid, To).
Decl export_from(Site, SourceModule, Name).
export_from(Site, SourceModule, Name) :- export_reexport(_, Site, SourceModule, Name, _).
Decl calls(Owner, Callee).
calls(Owner, Callee) :- site_owner(Site, Owner), call_edge(Site, Callee).
Decl references(Owner, Target).
references(Owner, Target) :- site_owner(Site, Owner), fn_ref(Site, Target).
Decl calls_reach(Owner, Callee).
calls_reach(A, B) :- calls(A, B).
calls_reach(A, C) :- calls(A, B), calls_reach(B, C).
Decl calls_or_refs_reach(Owner, Target).
calls_or_refs_reach(A, B) :- calls(A, B).
calls_or_refs_reach(A, B) :- references(A, B).
calls_or_refs_reach(A, C) :- calls(A, B), calls_or_refs_reach(B, C).
calls_or_refs_reach(A, C) :- references(A, B), calls_or_refs_reach(B, C).
Decl owner_uses_ambient(Owner, Source).
owner_uses_ambient(Owner, Source) :- site_owner(Site, Owner), ambient_use(Site, Source, _).
Decl owner_reaches_ambient(Owner, Source).
owner_reaches_ambient(Owner, Source) :- owner_uses_ambient(Owner, Source).
owner_reaches_ambient(Owner, Source) :- calls(Owner, Callee), owner_reaches_ambient(Callee, Source).
Decl owner_reaches_ambient_strict(Owner, Source).
owner_reaches_ambient_strict(Owner, Source) :- owner_uses_ambient(Owner, Source).
owner_reaches_ambient_strict(Owner, Source) :- calls(Owner, Callee), owner_reaches_ambient_strict(Callee, Source).
owner_reaches_ambient_strict(Owner, Source) :- references(Owner, Target), owner_reaches_ambient_strict(Target, Source).
Decl module_uses_ambient(File, Source).
module_uses_ambient(File, Source) :- site(Site, File), ambient_use(Site, Source, _).
Decl module_reaches_ambient(File, Source).
module_reaches_ambient(File, Source) :- module_uses_ambient(File, Source).
module_reaches_ambient(File, Source) :- module_imports(File, Mid), module_reaches_ambient(Mid, Source).
Decl owner_has_unresolved_call(Owner).
owner_has_unresolved_call(Owner) :- site_owner(Site, Owner), call_unresolved(Site).
Decl owner_reaches_unresolved(Owner).
owner_reaches_unresolved(Owner) :- owner_has_unresolved_call(Owner).
owner_reaches_unresolved(Owner) :- calls(Owner, Callee), owner_reaches_unresolved(Callee).
owner_reaches_unresolved(Owner) :- references(Owner, Target), owner_reaches_unresolved(Target).
Decl module_has_unresolved_call(File).
module_has_unresolved_call(File) :- site(Site, File), call_unresolved(Site).
Decl owner_calls_package(Owner, Pkg).
owner_calls_package(Owner, Pkg) :- site_owner(Site, Owner), external_call(Site, /package, Pkg, _).
Decl module_calls_package(File, Pkg).
module_calls_package(File, Pkg) :- site(Site, File), external_call(Site, /package, Pkg, _).
Decl type_child(Type, Child).
type_child(T, C) :- type_arg(T, _, C).
type_child(T, C) :- type_member(T, C).
type_child(T, C) :- type_prop(T, _, C).
type_child(T, C) :- type_sig_param(T, _, C).
type_child(T, C) :- type_sig_returns(T, C).
Decl type_contains(Type, Inner).
type_contains(T, C) :- type_child(T, C).
type_contains(T, D) :- type_child(T, C), type_contains(C, D).
Decl type_reaches_incomplete(Type).
type_reaches_incomplete(T) :- type_incomplete(T, _).
type_reaches_incomplete(T) :- type_child(T, C), type_reaches_incomplete(C).
Decl type_is_promise(Type).
type_is_promise(T) :- type_named(T, "lib", "Promise"), type_origin_kind(T, /lib).
Decl signature_returns_awaited(Signature, Type).
signature_returns_awaited(S, T) :- signature_returns(S, P), type_is_promise(P), type_arg(P, 0, T).
signature_returns_awaited(S, T) :- signature_returns(S, T), !type_is_promise(T).
