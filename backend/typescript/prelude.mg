# lintai prelude. These derived relations are available to every rule program.
# Rule programs cannot redefine them.

Decl glob(Path, Pattern)
  descr [
    external(),
    mode('+', '+'),
    doc("Path matches the doublestar pattern Pattern. Both arguments must be bound before glob is evaluated. glob cannot be negated: put it in a helper predicate and negate the helper."),
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
    doc("The import loads its target at runtime: it is not type-only, and it is a side-effect import or has at least one value binding."),
    arg(Imp, "Import entity id.")
  ].
import_runtime(Imp) :- import_edge(Imp, _, _, _), !import_type_only(Imp), !import_has_symbols(Imp).
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
