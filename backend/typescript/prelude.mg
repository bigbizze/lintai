# lintai prelude. These derived relations are available to every rule program.
# Rule programs cannot redefine them.

Decl import_has_symbols(Imp)
  descr [
    doc("The import declaration introduces at least one binding."),
    arg(Imp, "Import entity id."),
    arg_role(Imp, "entity"),
    arg_report_support(Imp, "optional")
  ].
import_has_symbols(Imp) :- import_symbol(Imp, _, _).

Decl export_from(Site, Module, Name)
  descr [
    doc("The immediate resolved source of one public export binding. Name is the source-side name, or * for a star/namespace export. Re-export chains are not expanded."),
    arg(Site, "Re-export syntax site."),
    arg(Module, "Resolved canonical source module path."),
    arg(Name, "Source-side public name or *."),
    arg_role(Site, "entity"),
    arg_report_support(Site, "optional"),
    arg_role(Module, "path")
  ].
export_from(Site, Module, Name) :- export_reexport(_, Site, Module, Name, _).

Decl import_runtime(Imp)
  descr [
    doc("The import loads its target at runtime: it is not type-only, and it is a side-effect import, has at least one value binding, or is kept by verbatimModuleSyntax (import_retained)."),
    arg(Imp, "Import entity id."),
    arg_role(Imp, "entity"),
    arg_report_support(Imp, "optional")
  ].
import_runtime(Imp) :- import_edge(Imp, _, _, _), !import_type_only(Imp), !import_has_symbols(Imp).
import_runtime(Imp) :- import_retained(Imp).
import_runtime(Imp) :- import_symbol(Imp, Name, _), !import_type_only(Imp), !import_symbol_type_only(Imp, Name).

Decl module_imports(From, To)
  descr [
    doc("From has a runtime import of To."),
    arg(From, "Path of the importing file."),
    arg(To, "Import target, as in import_edge."),
    arg_role(From, "path"),
    arg_role(To, "path")
  ].
module_imports(From, To) :- import_runtime(Imp), import_edge(Imp, From, To, _).

Decl imports_reach(From, To)
  descr [
    doc("Transitive closure of module_imports. It materializes every reachable pair and costs O(nodes^2) on large graphs. To check only whether a file reaches a target, use module_reaches_ambient or propagate backward from the target over module_imports."),
    arg(From, "Path of the importing file."),
    arg(To, "Import target reached through one or more runtime imports."),
    arg_role(From, "path"),
    arg_role(To, "path")
  ].
imports_reach(From, To) :- module_imports(From, To).
imports_reach(From, To) :- module_imports(From, Mid), imports_reach(Mid, To).

Decl calls_reach(Caller, Callee)
  descr [
    doc("Transitive closure of call_edge. It materializes every reachable pair and costs O(nodes^2) on large graphs. To check only whether a function reaches a target, use fn_reaches_ambient or propagate backward from the target over call_edge."),
    arg(Caller, "Function entity id, or module path for module-scope sites."),
    arg(Callee, "Function entity id reached through one or more calls."),
    arg_role(Caller, "entity"),
    arg_report_support(Caller, "optional"),
    arg_role(Callee, "entity"),
    arg_report_support(Callee, "optional")
  ].
calls_reach(A, B) :- call_edge(Site, A, B), !site_attribution_unknown(Site).
calls_reach(A, C) :- call_edge(Site, A, B), !site_attribution_unknown(Site), calls_reach(B, C).

Decl site_attribution_unknown(Site)
  descr [doc("Site has an owner that lintai cannot represent."), arg(Site, "Site entity id."),
    arg_role(Site, "entity"),
    arg_report_support(Site, "optional")
  ].
site_attribution_unknown(Site) :- site_attribution_unsupported(Site, _).

Decl fn_suspends(Fn)
  descr [doc("Fn owns an await, for await or await using site."), arg(Fn, "Function entity id."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional")
  ].
fn_suspends(Fn) :- suspension_in_fn(_, Fn).

Decl module_has_suspension(File)
  descr [doc("File contains a suspension site at any scope, including unsupported sites. This does not by itself mean module initialization suspends."), arg(File, "Workspace-relative path."),
    arg_role(File, "path")
  ].
module_has_suspension(File) :- suspension_site(_, File, _).

Decl fn_uses_ambient(Fn, Source)
  descr [
    doc("Fn contains a use of Source directly."),
    arg(Fn, "Function entity id."),
    arg(Source, "Source name constant."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional"),
    arg_representation(Source, "name")
  ].
fn_uses_ambient(Fn, Source) :- ambient_use(Site, Source, _, _), site_in_fn(Site, Fn).

Decl fn_reaches_ambient(Fn, Source)
  descr [
    doc("Fn uses Source directly or through a call chain."),
    arg(Fn, "Function entity id, or module path for module-scope sites."),
    arg(Source, "Source name constant."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional"),
    arg_representation(Source, "name")
  ].
fn_reaches_ambient(Fn, Source) :- fn_uses_ambient(Fn, Source).
fn_reaches_ambient(Fn, Source) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), fn_reaches_ambient(Callee, Source).

Decl module_uses_ambient(File, Source)
  descr [
    doc("File contains a use of Source, at any scope."),
    arg(File, "Path of the file."),
    arg(Source, "Source name constant."),
    arg_role(File, "path"),
    arg_representation(Source, "name")
  ].
module_uses_ambient(File, Source) :- ambient_use(_, Source, _, File).

Decl module_reaches_ambient(File, Source)
  descr [
    doc("File uses Source directly or through runtime imports."),
    arg(File, "Path of the file."),
    arg(Source, "Source name constant."),
    arg_role(File, "path"),
    arg_representation(Source, "name")
  ].
module_reaches_ambient(File, Source) :- module_uses_ambient(File, Source).
module_reaches_ambient(File, Source) :- module_imports(File, Mid), module_reaches_ambient(Mid, Source).

Decl calls_or_refs_reach(From, To)
  descr [
    doc("Follows call_edge and fn_ref together. A referenced function may never be called, and calls through function values can still be missing. It stores every connected pair, costing O(nodes^2) on large graphs. To check only whether a function reaches a target, use fn_reaches_ambient_strict or fn_reaches_unresolved, or propagate backward from the target."),
    arg(From, "Function entity id, or module path for module-scope sites."),
    arg(To, "Function entity id reached through calls or function-value references."),
    arg_role(From, "entity"),
    arg_report_support(From, "optional"),
    arg_role(To, "entity"),
    arg_report_support(To, "optional")
  ].
calls_or_refs_reach(A, B) :- call_edge(Site, A, B), !site_attribution_unknown(Site).
calls_or_refs_reach(A, B) :- fn_ref(Site, A, B), !site_attribution_unknown(Site).
calls_or_refs_reach(A, C) :- call_edge(Site, A, B), !site_attribution_unknown(Site), calls_or_refs_reach(B, C).
calls_or_refs_reach(A, C) :- fn_ref(Site, A, B), !site_attribution_unknown(Site), calls_or_refs_reach(B, C).

Decl fn_has_unresolved_call(Fn)
  descr [
    doc("Fn directly contains a call the checker could not resolve."),
    arg(Fn, "Function entity id."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional")
  ].
fn_has_unresolved_call(Fn) :- call_unresolved(Site, _), site_in_fn(Site, Fn).

Decl module_has_unresolved_call(File)
  descr [
    doc("File contains an unresolved call at any scope."),
    arg(File, "Path of the file."),
    arg_role(File, "path")
  ].
module_has_unresolved_call(File) :- call_unresolved(Site, _), location(Site, File, _, _, _, _).

Decl fn_reaches_unresolved(Fn)
  descr [
    doc("Fn contains an unresolved call, directly or through call_edge and fn_ref steps. A strict rule treats this as leaving the analyzed code."),
    arg(Fn, "Function entity id, or module path for module-scope sites."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional")
  ].
fn_reaches_unresolved(Fn) :- fn_has_unresolved_call(Fn).
fn_reaches_unresolved(Fn) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), fn_reaches_unresolved(Callee).
fn_reaches_unresolved(Fn) :- fn_ref(Site, Fn, Callee), !site_attribution_unknown(Site), fn_reaches_unresolved(Callee).

Decl fn_reaches_ambient_strict(Fn, Source)
  descr [
    doc("Like fn_reaches_ambient, but also follows fn_ref steps."),
    arg(Fn, "Function entity id, or module path for module-scope sites."),
    arg(Source, "Source name constant."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional"),
    arg_representation(Source, "name")
  ].
fn_reaches_ambient_strict(Fn, Source) :- fn_uses_ambient(Fn, Source).
fn_reaches_ambient_strict(Fn, Source) :- call_edge(Site, Fn, Callee), !site_attribution_unknown(Site), fn_reaches_ambient_strict(Callee, Source).
fn_reaches_ambient_strict(Fn, Source) :- fn_ref(Site, Fn, Callee), !site_attribution_unknown(Site), fn_reaches_ambient_strict(Callee, Source).

Decl fn_calls_package(Fn, Pkg)
  descr [
    doc("Fn directly calls into a package. lib calls are excluded."),
    arg(Fn, "Function entity id."),
    arg(Pkg, "Package name from external_call."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional")
  ].
fn_calls_package(Fn, Pkg) :- external_call(Site, _, Pkg, _), site_in_fn(Site, Fn), Pkg != "lib".

Decl module_calls_package(File, Pkg)
  descr [
    doc("File directly calls into a package, at any scope."),
    arg(File, "Path of the file."),
    arg(Pkg, "Package name from external_call."),
    arg_role(File, "path")
  ].
module_calls_package(File, Pkg) :- external_call(Site, _, Pkg, _), Pkg != "lib", location(Site, File, _, _, _, _).

Decl type_child(Type, Child)
  descr [
    doc("Child appears directly in Type: a type argument, tuple element, union or intersection member, property type, signature parameter or signature return."),
    arg(Type, "Type entity id."),
    arg(Child, "Type entity id."),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg_role(Child, "entity"),
    arg_report_support(Child, "optional")
  ].
type_child(T, C) :- type_arg(T, _, C).
type_child(T, C) :- type_member(T, C).
type_child(T, C) :- type_prop(T, _, C).
type_child(T, C) :- type_sig_param(T, _, C).
type_child(T, C) :- type_sig_returns(T, C).

Decl type_contains(Type, Inner)
  descr [
    doc("Transitive closure of type_child. It materializes every reachable pair and costs O(nodes^2) on large graphs. To check only whether a type contains a target, use type_reaches_incomplete or propagate backward from the target over type_child. It does not look inside class, interface or enum members, into the call, construct or index signatures of an /object, into type parameter constraints, or into /other, truncated or conflicting types. Named classes, interfaces and enums receive /members_omitted, so type_reaches_incomplete can fail closed on them."),
    arg(Type, "Type entity id."),
    arg(Inner, "Type entity id reached through one or more type_child steps."),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg_role(Inner, "entity"),
    arg_report_support(Inner, "optional")
  ].
type_contains(T, C) :- type_child(T, C).
type_contains(T, D) :- type_child(T, C), type_contains(C, D).

Decl type_reaches_incomplete(Type)
  descr [
    doc("Type, or a type that it contains through type_child steps, has a type_incomplete row. This includes named classes, interfaces and enums marked /members_omitted because their members are not inspected."),
    arg(Type, "Type entity id."),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].
type_reaches_incomplete(T) :- type_incomplete(T, _).
type_reaches_incomplete(T) :- type_child(T, C), type_reaches_incomplete(C).

Decl type_is_promise(Type)
  descr [
    doc("Type is the default library's Promise, with any type argument. A Promise declared in a declare module \"lib\" block is not."),
    arg(Type, "Type entity id."),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].
type_is_promise(T) :- type_named(T, "lib", "Promise"), type_origin_kind(T, /lib).

Decl fn_returns_awaited(Fn, Type)
  descr [
    doc("The return type of Fn with exactly one level of the lib Promise removed: the type argument of a returned Promise, otherwise the return type itself. This is not TypeScript's Awaited: it does not unwrap PromiseLike or other thenables, a Promise inside a Promise, or a union that contains a Promise, so Promise<A> | undefined is returned unchanged."),
    arg(Fn, "Function entity id."),
    arg(Type, "Type entity id."),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "optional"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].
fn_returns_awaited(Fn, T) :- fn_returns(Fn, P), type_is_promise(P), type_arg(P, 0, T).
fn_returns_awaited(Fn, T) :- fn_returns(Fn, T), !type_is_promise(T).
