# lintai prelude. These derived relations are available to every rule program.
# Rule programs cannot redefine them.

Decl glob(Path, Pattern)
  descr [
    external(),
    mode('+', '+'),
    doc("Path matches the doublestar pattern Pattern. Both arguments must be bound by earlier premises. !glob(Path, Pattern) holds when Path does not match."),
    arg(Path, "Workspace-relative path."),
    arg(Pattern, "Doublestar pattern, for example \"src/core/**\".")
  ].

Decl import_has_symbols(Imp)
  descr [
    doc("The import declaration introduces at least one binding."),
    arg(Imp, "Import entity id.")
  ].
import_has_symbols(Imp) :- import_symbol(Imp, _, _).

Decl import_runtime(Imp)
  descr [
    doc("The import loads its target at runtime: it is not type-only, and it is a side-effect import, has at least one value binding, or is kept by verbatimModuleSyntax (import_retained)."),
    arg(Imp, "Import entity id.")
  ].
import_runtime(Imp) :- import_edge(Imp, _, _, _), !import_type_only(Imp), !import_has_symbols(Imp).
import_runtime(Imp) :- import_retained(Imp).
import_runtime(Imp) :- import_symbol(Imp, Name, _), !import_type_only(Imp), !import_symbol_type_only(Imp, Name).

Decl module_imports(From, To)
  descr [
    doc("From has a runtime import of To."),
    arg(From, "Path of the importing file."),
    arg(To, "Import target, as in import_edge.")
  ].
module_imports(From, To) :- import_runtime(Imp), import_edge(Imp, From, To, _).

Decl imports_reach(From, To)
  descr [
    doc("Transitive closure of module_imports."),
    arg(From, "Path of the importing file."),
    arg(To, "Import target reached through one or more runtime imports.")
  ].
imports_reach(From, To) :- module_imports(From, To).
imports_reach(From, To) :- module_imports(From, Mid), imports_reach(Mid, To).

Decl calls_reach(Caller, Callee)
  descr [
    doc("Transitive closure of call_edge."),
    arg(Caller, "Function entity id."),
    arg(Callee, "Function entity id reached through one or more calls.")
  ].
calls_reach(A, B) :- call_edge(_, A, B).
calls_reach(A, C) :- call_edge(_, A, B), calls_reach(B, C).

Decl fn_uses_ambient(Fn, Source)
  descr [
    doc("Fn contains a use of Source directly."),
    arg(Fn, "Function entity id."),
    arg(Source, "Source name constant.")
  ].
fn_uses_ambient(Fn, Source) :- ambient_use(Site, Source, _, _), site_in_fn(Site, Fn).

Decl fn_reaches_ambient(Fn, Source)
  descr [
    doc("Fn uses Source directly or through a call chain."),
    arg(Fn, "Function entity id."),
    arg(Source, "Source name constant.")
  ].
fn_reaches_ambient(Fn, Source) :- fn_uses_ambient(Fn, Source).
fn_reaches_ambient(Fn, Source) :- calls_reach(Fn, Callee), fn_uses_ambient(Callee, Source).

Decl module_uses_ambient(File, Source)
  descr [
    doc("File contains a use of Source, at any scope."),
    arg(File, "Path of the file."),
    arg(Source, "Source name constant.")
  ].
module_uses_ambient(File, Source) :- ambient_use(_, Source, _, File).

Decl module_reaches_ambient(File, Source)
  descr [
    doc("File uses Source directly or through runtime imports."),
    arg(File, "Path of the file."),
    arg(Source, "Source name constant.")
  ].
module_reaches_ambient(File, Source) :- module_uses_ambient(File, Source).
module_reaches_ambient(File, Source) :- imports_reach(File, Mid), module_uses_ambient(Mid, Source).

Decl calls_or_refs_reach(From, To)
  descr [
    doc("Transitive closure over call_edge and fn_ref together: a conservative over-approximation of workspace-internal reachability."),
    arg(From, "Function entity id, or module path for module-scope sites."),
    arg(To, "Function entity id reached through calls or function-value references.")
  ].
calls_or_refs_reach(A, B) :- call_edge(_, A, B).
calls_or_refs_reach(A, B) :- fn_ref(_, A, B).
calls_or_refs_reach(A, C) :- call_edge(_, A, B), calls_or_refs_reach(B, C).
calls_or_refs_reach(A, C) :- fn_ref(_, A, B), calls_or_refs_reach(B, C).

Decl fn_has_unresolved_call(Fn)
  descr [
    doc("Fn directly contains a call the checker could not resolve."),
    arg(Fn, "Function entity id.")
  ].
fn_has_unresolved_call(Fn) :- call_unresolved(Site, _), site_in_fn(Site, Fn).

Decl module_has_unresolved_call(File)
  descr [
    doc("File contains an unresolved call at any scope."),
    arg(File, "Path of the file.")
  ].
module_has_unresolved_call(File) :- call_unresolved(Site, _), location(Site, File, _, _, _, _).

Decl fn_reaches_unresolved(Fn)
  descr [
    doc("Fn contains an unresolved call, directly or through calls_or_refs_reach. A strict rule treats this as leaving the analyzed code."),
    arg(Fn, "Function entity id.")
  ].
fn_reaches_unresolved(Fn) :- fn_has_unresolved_call(Fn).
fn_reaches_unresolved(Fn) :- calls_or_refs_reach(Fn, Callee), fn_has_unresolved_call(Callee).

Decl fn_reaches_ambient_strict(Fn, Source)
  descr [
    doc("Like fn_reaches_ambient, but over calls_or_refs_reach."),
    arg(Fn, "Function entity id."),
    arg(Source, "Source name constant.")
  ].
fn_reaches_ambient_strict(Fn, Source) :- fn_uses_ambient(Fn, Source).
fn_reaches_ambient_strict(Fn, Source) :- calls_or_refs_reach(Fn, Callee), fn_uses_ambient(Callee, Source).

Decl fn_calls_package(Fn, Pkg)
  descr [
    doc("Fn directly calls into a package. lib calls are excluded."),
    arg(Fn, "Function entity id."),
    arg(Pkg, "Package name from external_call.")
  ].
fn_calls_package(Fn, Pkg) :- external_call(Site, _, Pkg, _), site_in_fn(Site, Fn), Pkg != "lib".

Decl module_calls_package(File, Pkg)
  descr [
    doc("File directly calls into a package, at any scope."),
    arg(File, "Path of the file."),
    arg(Pkg, "Package name from external_call.")
  ].
module_calls_package(File, Pkg) :- external_call(Site, _, Pkg, _), Pkg != "lib", location(Site, File, _, _, _, _).

Decl type_child(Type, Child)
  descr [
    doc("Child appears directly in Type: a type argument, tuple element, union or intersection member, property type, signature parameter or signature return."),
    arg(Type, "Type entity id."),
    arg(Child, "Type entity id.")
  ].
type_child(T, C) :- type_arg(T, _, C).
type_child(T, C) :- type_member(T, C).
type_child(T, C) :- type_prop(T, _, C).
type_child(T, C) :- type_sig_param(T, _, C).
type_child(T, C) :- type_sig_returns(T, C).

Decl type_contains(Type, Inner)
  descr [
    doc("Transitive closure of type_child. Does not look inside classes, interfaces or enums, whose members are not emitted."),
    arg(Type, "Type entity id."),
    arg(Inner, "Type entity id reached through one or more type_child steps.")
  ].
type_contains(T, C) :- type_child(T, C).
type_contains(T, D) :- type_child(T, C), type_contains(C, D).

Decl type_is_promise(Type)
  descr [
    doc("Type is the default library's Promise, with any type argument."),
    arg(Type, "Type entity id.")
  ].
type_is_promise(T) :- type_named(T, "lib", "Promise").

Decl fn_returns_awaited(Fn, Type)
  descr [
    doc("The return type of Fn with one level of Promise removed: the type argument of a returned Promise, otherwise the return type itself."),
    arg(Fn, "Function entity id."),
    arg(Type, "Type entity id.")
  ].
fn_returns_awaited(Fn, T) :- fn_returns(Fn, P), type_is_promise(P), type_arg(P, 0, T).
fn_returns_awaited(Fn, T) :- fn_returns(Fn, T), !type_is_promise(T).
