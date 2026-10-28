# lintai base relations. lintai extracts these facts from the workspace.
# Paths are workspace-relative strings with forward slashes.
# Entity ids are semantic keys, as strings. A module's entity id is its path.
# Booleans are presence facts. Closed vocabularies are name constants.

Decl module(File)
  descr [
    extensional(),
    doc("A source file in the workspace."),
    arg(File, "Workspace-relative path. This is also the module's entity id.")
  ].

Decl fn(Fn, File, Name)
  descr [
    extensional(),
    doc("A collected function, method, arrow function or function expression."),
    arg(Fn, "Function entity id."),
    arg(File, "Path of the file that declares the function."),
    arg(Name, "Display name, for example Container.local.")
  ].

Decl fn_kind(Fn, Kind)
  descr [
    extensional(),
    doc("The syntactic kind of a function."),
    arg(Fn, "Function entity id."),
    arg(Kind, "Kind string, for example \"function\", \"method\" or \"arrow\".")
  ].

Decl fn_container(Fn, Container)
  descr [
    extensional(),
    doc("The name of the class or object that contains a function. Only present when the name is not empty."),
    arg(Fn, "Function entity id."),
    arg(Container, "Container name.")
  ].

Decl fn_exported(Fn)
  descr [
    extensional(),
    doc("The function is exported from its module."),
    arg(Fn, "Function entity id.")
  ].

Decl fn_async(Fn)
  descr [
    extensional(),
    doc("The function has the async modifier."),
    arg(Fn, "Function entity id.")
  ].

Decl fn_awaits(Fn)
  descr [
    extensional(),
    doc("The function body contains an await expression."),
    arg(Fn, "Function entity id.")
  ].

Decl fn_param(Fn, Index, TypeText)
  descr [
    extensional(),
    doc("One row per function parameter."),
    arg(Fn, "Function entity id."),
    arg(Index, "Parameter position, starting at 0."),
    arg(TypeText, "Parameter type as text.")
  ].

Decl fn_returns(Fn, TypeText)
  descr [
    extensional(),
    doc("The return type of a function."),
    arg(Fn, "Function entity id."),
    arg(TypeText, "Return type as text.")
  ].

Decl import_edge(Imp, From, To, Specifier)
  descr [
    extensional(),
    doc("One row per import declaration."),
    arg(Imp, "Import entity id."),
    arg(From, "Path of the importing file."),
    arg(To, "Workspace-relative path when the import resolves inside the workspace, otherwise the specifier."),
    arg(Specifier, "The module specifier as written.")
  ].

Decl import_internal(Imp)
  descr [
    extensional(),
    doc("The import resolves to a file inside the workspace."),
    arg(Imp, "Import entity id.")
  ].

Decl import_type_only(Imp)
  descr [
    extensional(),
    doc("The declaration is an import type declaration."),
    arg(Imp, "Import entity id.")
  ].

Decl import_symbol(Imp, Name, Kind)
  descr [
    extensional(),
    doc("A binding that an import declaration introduces. A side-effect import has no rows."),
    arg(Imp, "Import entity id."),
    arg(Name, "Imported name."),
    arg(Kind, "/default, /namespace or /named.")
  ].

Decl import_symbol_type_only(Imp, Name)
  descr [
    extensional(),
    doc("A named import with the type modifier, as in import { type X }."),
    arg(Imp, "Import entity id."),
    arg(Name, "Imported name.")
  ].

Decl call_edge(Call, Caller, Callee)
  descr [
    extensional(),
    doc("A resolved call from one collected function to another."),
    arg(Call, "Call entity id."),
    arg(Caller, "Entity id of the calling function."),
    arg(Callee, "Entity id of the called function.")
  ].

Decl type_ref(Ref, File, Name)
  descr [
    extensional(),
    doc("A type reference."),
    arg(Ref, "Type reference entity id."),
    arg(File, "Path of the file that contains the reference."),
    arg(Name, "Referenced type name.")
  ].

Decl type_ref_target(Ref, TargetFile)
  descr [
    extensional(),
    doc("The file that declares the referenced type. Only present when that file is inside the workspace."),
    arg(Ref, "Type reference entity id."),
    arg(TargetFile, "Path of the declaring file.")
  ].

Decl meta_access(Acc, File, AccessPath)
  descr [
    extensional(),
    doc("An import.meta member access."),
    arg(Acc, "Access entity id."),
    arg(File, "Path of the file that contains the access."),
    arg(AccessPath, "Accessed path, for example \"import.meta.env\".")
  ].

Decl ambient_source(Source, Capability)
  descr [
    extensional(),
    doc("One row per capability of every registered ambient source."),
    arg(Source, "Source name constant, for example /clock."),
    arg(Capability, "Capability name constant, for example /now.")
  ].

Decl ambient_use(Site, Source, Capability, File)
  descr [
    extensional(),
    doc("A use of an ambient source."),
    arg(Site, "Site entity id."),
    arg(Source, "Source name constant."),
    arg(Capability, "Capability name constant."),
    arg(File, "Path of the file that contains the site.")
  ].

Decl ambient_key(Site, Key)
  descr [
    extensional(),
    doc("The static key that the site reads or writes."),
    arg(Site, "Site entity id."),
    arg(Key, "Key string.")
  ].

Decl ambient_dynamic(Site)
  descr [
    extensional(),
    doc("The key at the site is not static, or the whole object escapes."),
    arg(Site, "Site entity id.")
  ].

Decl ambient_write(Site)
  descr [
    extensional(),
    doc("The site assigns to or deletes the keyed member."),
    arg(Site, "Site entity id.")
  ].

Decl site_in_fn(Site, Fn)
  descr [
    extensional(),
    doc("The innermost collected function that contains a call, ambient-use or meta-access site. Sites at module scope have no row."),
    arg(Site, "Site entity id."),
    arg(Fn, "Function entity id.")
  ].

Decl location(Entity, File, StartLine, StartCol, EndLine, EndCol)
  descr [
    extensional(),
    doc("The source range of an entity. Every entity id in the base relations has one row."),
    arg(Entity, "Entity id."),
    arg(File, "Path of the file."),
    arg(StartLine, "1-based start line."),
    arg(StartCol, "1-based start column."),
    arg(EndLine, "1-based end line."),
    arg(EndCol, "1-based end column.")
  ].
