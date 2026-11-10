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
    arg(Kind, "\"function\", \"function_expression\", \"arrow_function\", \"method\", \"constructor\", \"get_accessor\" or \"set_accessor\".")
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
    doc("An ordinary await expression belongs to this function, including its parameter defaults."),
    arg(Fn, "Function entity id.")
  ].

Decl fn_signature_omitted(Fn)
  descr [extensional(), doc("Signature collection was skipped for an anonymous ownership-only function."), arg(Fn, "Function entity id.")].

Decl fn_signature_unresolved(Fn)
  descr [extensional(), doc("Signature collection was attempted but the checker did not provide a complete signature."), arg(Fn, "Function entity id.")].

Decl suspension_site(Site, File, Kind)
  descr [
    extensional(),
    doc("One syntax site for ordinary await, for await or await using. This does not prove execution."),
    arg(Site, "Suspension site entity id."),
    arg(File, "Workspace-relative path."),
    arg(Kind, "/await, /for_await or /await_using.")
  ].

Decl suspension_in_fn(Site, Fn)
  descr [extensional(), doc("The suspension site belongs to this function."), arg(Site, "Suspension site entity id."), arg(Fn, "Function entity id.")].

Decl suspension_at_module(Site)
  descr [extensional(), doc("The suspension site belongs to module evaluation."), arg(Site, "Suspension site entity id.")].

Decl site_attribution_unsupported(Site, Reason)
  descr [
    extensional(),
    doc("The site's lexical owner cannot be represented. It has no function or module owner."),
    arg(Site, "Site entity id."),
    arg(Reason, "/instance_construction or /unsupported_context.")
  ].

Decl fn_param(Fn, Index, Type)
  descr [
    extensional(),
    doc("One row per function parameter."),
    arg(Fn, "Function entity id."),
    arg(Index, "Parameter position, starting at 0."),
    arg(Type, "Type entity id of the parameter.")
  ].

Decl fn_returns(Fn, Type)
  descr [
    extensional(),
    doc("The return type of a function."),
    arg(Fn, "Function entity id."),
    arg(Type, "Type entity id of the return type.")
  ].

Decl import_edge(Imp, From, To, Specifier)
  descr [
    extensional(),
    doc("One row per module reference with a string specifier: import declarations, export ... from, import(), require(), import x = require() and typeof import(). import_form gives the form."),
    arg(Imp, "Import entity id."),
    arg(From, "Path of the importing file."),
    arg(To, "Workspace-relative path when import_internal is set, otherwise the specifier."),
    arg(Specifier, "The module specifier as written.")
  ].

Decl import_form(Imp, Form)
  descr [
    extensional(),
    doc("The syntax of a module reference. Every import_edge has one row."),
    arg(Imp, "Import entity id."),
    arg(Form, "/static (import declaration), /reexport (export ... from), /dynamic (import()), /require (require()), /import_equals (import x = require()) or /import_type (typeof import()).")
  ].

Decl import_internal(Imp)
  descr [
    extensional(),
    doc("The import resolves to a workspace file that is not in node_modules."),
    arg(Imp, "Import entity id.")
  ].

Decl import_unresolved(Imp)
  descr [
    extensional(),
    doc("The import resolves to no file and to no declared module. A typo in a path or an alias, or a package that is not installed, gives this fact."),
    arg(Imp, "Import entity id.")
  ].

Decl import_package(Imp, Pkg)
  descr [
    extensional(),
    doc("The package name of an import that resolves to a package or to a declared module, for example \"next\" for \"next/server\" and \"@scope/pkg\" for \"@scope/pkg/sub\". Also present for an internal import that resolves through a workspace package."),
    arg(Imp, "Import entity id."),
    arg(Pkg, "Package name.")
  ].

Decl import_type_only(Imp)
  descr [
    extensional(),
    doc("The reference loads only types: import type, export type ... from, or typeof import()."),
    arg(Imp, "Import entity id.")
  ].

Decl import_retained(Imp)
  descr [
    extensional(),
    doc("An import declaration whose bindings are all type-only, as in import { type X } from \"m\", when verbatimModuleSyntax is on. tsc emits it as import {} from \"m\", so the target loads at runtime."),
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
    doc("A call to a collected function. The callee is the declaration that the type checker resolves for the call. A call through a value whose type is a function type, for example a callback parameter or a property of a record of functions, has no row; it is a call_unresolved row."),
    arg(Call, "Call entity id."),
    arg(Caller, "Calling function, module path at module scope, or an empty string when attribution is unsupported."),
    arg(Callee, "Entity id of the called function.")
  ].

Decl external_call(Call, Caller, Module, Name)
  descr [
    extensional(),
    doc("A call whose resolved declaration is outside the workspace source files: in the default library, in a package or in a declaration file."),
    arg(Call, "Call entity id."),
    arg(Caller, "Calling function, module path at module scope, or an empty string when attribution is unsupported."),
    arg(Module, "\"lib\" for the default library, the package name for a package (the @types/ prefix is removed), the module name of a declare module block, or the path of a workspace declaration file."),
    arg(Name, "Name of the declaration with its containers, for example \"JSON.parse\", \"Set.add\" or \"DateConstructor.now\".")
  ].

Decl call_unresolved(Site, Caller)
  descr [
    extensional(),
    doc("A call that lintai cannot follow: a call, new or tagged template whose target is not a retained workspace function or an external declaration, including a function in a file excluded by the file scope; f.call() or f.apply() when f is not a collected function; a function value that is not a collected function, passed to an external call (the row is at the argument); new of a workspace class without a constructor whose fields have initializers that run code. Every call site has exactly one of: call_edge, external_call, call_unresolved."),
    arg(Site, "Call site entity id."),
    arg(Caller, "Calling function, module path at module scope, or an empty string when attribution is unsupported.")
  ].

Decl call_declaration(Site, Decl)
  descr [
    extensional(),
    doc("The checked program selects this concrete signature declaration for the invocation. This evidence is independent of runtime target resolution. Differing program observations may retain multiple declarations with /conflict."),
    arg(Site, "Existing call_edge, external_call or call_unresolved site SemanticKey, without an EntityID prefix; reuses its location and ownership."),
    arg(Decl, "Canonical checked signature declaration entity id.")
  ].

Decl declaration_member(Decl, OwnerDecl, Name)
  descr [
    extensional(),
    doc("The signature declaration belongs lexically to this named member of the owner declaration, independently of any call-site receiver. Absence of a row does not establish safety or absence of named member access."),
    arg(Decl, "Canonical signature declaration entity id."),
    arg(OwnerDecl, "Canonical lexical owner declaration entity id."),
    arg(Name, "Member name.")
  ].

Decl call_declaration_incomplete(Site, Reason)
  descr [
    extensional(),
    doc("Declaration or member provenance is incomplete for this invocation. Partial concrete declaration evidence may coexist with an incomplete outcome; absence of this marker is not a general proof of type safety."),
    arg(Site, "Existing call site SemanticKey, without an EntityID prefix; reuses its location and ownership."),
    arg(Reason, "/unresolved: no usable selected signature; /synthetic: synthesized signature without one concrete declaration; /unsupported: signature or member shape cannot be represented safely; /conflict: checked-program observations disagree about declaration or member evidence.")
  ].

Decl import_opaque(Site, From)
  descr [
    extensional(),
    doc("A dynamic import() or require() whose specifier is not a string literal. These references have no import_edge row; this fact makes them visible."),
    arg(Site, "Reference site entity id."),
    arg(From, "Path of the file that contains the reference.")
  ].

Decl fn_ref(Ref, From, To)
  descr [
    extensional(),
    doc("A collected function used as a value, for example passed as an argument, stored, returned or used as a property value. An inline arrow function, function expression or object literal method creates a reference from its surrounding owner. A nested function declaration alone does not create a reference. Import and export specifiers are not references."),
    arg(Ref, "Reference entity id."),
    arg(From, "Surrounding function, module path at module scope, or an empty string when attribution is unsupported."),
    arg(To, "Entity id of the referenced function.")
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

Decl type_kind(Type, Kind)
  descr [
    extensional(),
    doc("The structure of a type. Every type entity has one row. A named type also has a type_named row; the kind describes what the name refers to."),
    arg(Type, "Type entity id."),
    arg(Kind, "/any, /unknown, /never, /void, /undefined, /null, /string, /number, /bigint, /boolean, /symbol, /object_keyword, /literal, /type_param, /union, /intersection, /tuple, /function, /object or /other.")
  ].

Decl type_named(Type, Origin, Name)
  descr [
    extensional(),
    doc("The type is a class, interface, enum or type alias, or an instantiation of one. Two types with the same Origin and Name and different type arguments are different entities."),
    arg(Type, "Type entity id."),
    arg(Origin, "\"lib\" for the default library, the package name for a package (the @types/ prefix is removed), the module name of a declare module block, or the workspace-relative path of the declaring file. Matches the Module argument of external_call. type_origin_kind tells these apart."),
    arg(Name, "Name with its containing namespaces, for example \"Promise\" or \"Models.User\".")
  ].

Decl type_arg(Type, Index, Arg)
  descr [
    extensional(),
    doc("A type argument of a named type, or an element of a tuple. The this-type argument that the checker adds to class and interface references is not included."),
    arg(Type, "Type entity id."),
    arg(Index, "Position, starting at 0."),
    arg(Arg, "Type entity id of the argument.")
  ].

Decl type_member(Type, Member)
  descr [
    extensional(),
    doc("A member of a union or intersection. Members are unordered."),
    arg(Type, "Type entity id of the union or intersection."),
    arg(Member, "Type entity id of the member.")
  ].

Decl type_literal(Type, Value)
  descr [
    extensional(),
    doc("The value of a literal type."),
    arg(Type, "Type entity id."),
    arg(Value, "A string for a string literal, a number for a number literal, /true or /false for a boolean literal, and the decimal digits as a string for a bigint literal.")
  ].

Decl type_param_name(Type, Name)
  descr [
    extensional(),
    doc("The name of a type parameter."),
    arg(Type, "Type entity id."),
    arg(Name, "Type parameter name, for example \"T\".")
  ].

Decl type_prop(Type, Name, PropType)
  descr [
    extensional(),
    doc("A property of an anonymous object type. Properties of classes and interfaces are not emitted."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name."),
    arg(PropType, "Type entity id of the property.")
  ].

Decl type_prop_optional(Type, Name)
  descr [
    extensional(),
    doc("The property is optional."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name.")
  ].

Decl type_sig_param(Type, Index, ParamType)
  descr [
    extensional(),
    doc("A parameter of a /function type."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0."),
    arg(ParamType, "Type entity id of the parameter.")
  ].

Decl type_sig_returns(Type, ReturnType)
  descr [
    extensional(),
    doc("The return type of a /function type."),
    arg(Type, "Type entity id."),
    arg(ReturnType, "Type entity id of the return type.")
  ].

Decl type_origin_kind(Type, Kind)
  descr [
    extensional(),
    doc("The kind of the origin of a named type. The Origin of type_named alone is ambiguous: a declare module block can have the name \"lib\" or the path of a workspace file."),
    arg(Type, "Type entity id."),
    arg(Kind, "/lib, /package, /ambient_module, /declaration_file or /source_file.")
  ].

Decl type_arg_optional(Type, Index)
  descr [
    extensional(),
    doc("The tuple element is optional."),
    arg(Type, "Type entity id of the tuple."),
    arg(Index, "Element position, starting at 0.")
  ].

Decl type_arg_rest(Type, Index)
  descr [
    extensional(),
    doc("The tuple element is a rest element."),
    arg(Type, "Type entity id of the tuple."),
    arg(Index, "Element position, starting at 0.")
  ].

Decl type_readonly(Type)
  descr [
    extensional(),
    doc("The tuple is readonly. A readonly array is the lib interface ReadonlyArray."),
    arg(Type, "Type entity id of the tuple.")
  ].

Decl type_prop_readonly(Type, Name)
  descr [
    extensional(),
    doc("The property is readonly."),
    arg(Type, "Type entity id."),
    arg(Name, "Property name.")
  ].

Decl type_sig_param_optional(Type, Index)
  descr [
    extensional(),
    doc("The parameter of a /function type is optional."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0.")
  ].

Decl type_sig_param_rest(Type, Index)
  descr [
    extensional(),
    doc("The parameter of a /function type is a rest parameter."),
    arg(Type, "Type entity id."),
    arg(Index, "Parameter position, starting at 0.")
  ].

Decl type_incomplete(Type, Reason)
  descr [
    extensional(),
    doc("The facts of the type leave out structure that a rule can depend on: /truncated, /unsupported, /signatures, /constraint, /conflict or /members_omitted. A rule that must see every type inside a signature can use type_reaches_incomplete."),
    arg(Type, "Type entity id."),
    arg(Reason, "/truncated (more than 16 levels), /unsupported (an /other type, or a type parameter with no declaration), /signatures (an /object with call, construct or index signatures), /constraint (a type parameter with a constraint), /conflict (config groups give different structure) or /members_omitted (a named class, interface or enum whose members are not inspected).")
  ].

Decl type_display(Type, Text)
  descr [
    extensional(),
    doc("The type as the TypeScript 7 checker prints it. For messages and debugging. Do not match rules against it; use the structural relations."),
    arg(Type, "Type entity id."),
    arg(Text, "Type text.")
  ].

Decl declaration(Declaration, Name, Kind)
  descr [
    extensional(),
    doc("An actual declaration endpoint observed by the TypeScript checker. Overloads and merged declarations have separate IDs."),
    arg(Declaration, "Canonical declaration entity id."),
    arg(Name, "Qualified declaration name, or default for an anonymous default declaration."),
    arg(Kind, "/function, /variable, /type_alias, /interface, /class, /enum, /namespace, /method, /property, /signature or /export_expression.")
  ].

Decl declaration_origin(Declaration, OriginKind, Origin)
  descr [
    extensional(),
    doc("Descriptive origin of an actual declaration. Origin is not part of declaration identity."),
    arg(Declaration, "Canonical declaration entity id."),
    arg(OriginKind, "/source_file, /declaration_file, /package, /ambient_module or /lib."),
    arg(Origin, "Workspace path, package name/version, ambient module or library label.")
  ].

Decl declaration_type(Declaration, Space, Type)
  descr [
    extensional(),
    doc("A checker-observed type association for a declaration. A class can have separate type and value-space associations."),
    arg(Declaration, "Canonical declaration entity id."),
    arg(Space, "/type or /value."),
    arg(Type, "Type entity id.")
  ].

Decl declaration_fn(Declaration, Fn)
  descr [
    extensional(),
    doc("Direct correspondence from a declaration endpoint to an already collected function."),
    arg(Declaration, "Canonical declaration entity id."),
    arg(Fn, "Function entity id.")
  ].

Decl declaration_incomplete(Declaration, Reason)
  descr [
    extensional(),
    doc("The declaration's checker type or direct function association is unresolved, unsupported or conflicted. Optional source recovery failure is reported separately."),
    arg(Declaration, "Canonical declaration entity id."),
    arg(Reason, "/unresolved, /unsupported or /conflict.")
  ].

Decl module_export(Export, File, Name, Space)
  descr [
    extensional(),
    doc("A checker-visible public binding of a retained analyzed module."),
    arg(Export, "Canonical public export entity id."),
    arg(File, "Analyzed module path."),
    arg(Name, "Public export name; default and export= are explicit names."),
    arg(Space, "/type or /value.")
  ].

Decl export_target(Export, Declaration)
  descr [
    extensional(),
    doc("An actual declaration endpoint of a public binding. Overload and merge members are separate set members and are not paired."),
    arg(Export, "Public export entity id."),
    arg(Declaration, "Canonical declaration entity id.")
  ].

Decl export_via(Export, Site)
  descr [
    extensional(),
    doc("Syntax site contributing to a public binding, including a local export statement."),
    arg(Export, "Public export entity id."),
    arg(Site, "Import/re-export statement or deterministic local export-site id.")
  ].

Decl export_reexport(Export, Site, Module, SourceName, Space)
  descr [
    extensional(),
    doc("A binding-specific immediate re-export edge. Module is the resolved canonical module identity; star and namespace forms use source name *."),
    arg(Export, "Public export entity id."),
    arg(Site, "Re-export syntax site."),
    arg(Module, "Resolved canonical source module path."),
    arg(SourceName, "Immediate source-side name or * for star and namespace forms."),
    arg(Space, "/type or /value.")
  ].

Decl export_incomplete(Export, Reason)
  descr [
    extensional(),
    doc("A known public binding has an unresolved, unsupported or conflicted target. Known positive targets remain available."),
    arg(Export, "Public export entity id."),
    arg(Reason, "/unresolved, /unsupported or /conflict.")
  ].

Decl module_exports_incomplete(File, Reason)
  descr [
    extensional(),
    doc("The module's public-name inventory is incomplete or conflicted. Absence is not a safe result while this fact applies."),
    arg(File, "Analyzed module path."),
    arg(Reason, "/unresolved, /unsupported or /conflict.")
  ].

Decl fn_signature_conflict(Fn)
  descr [
    extensional(),
    doc("Complete checker observations for this function disagree as full parameter and return signatures. Flat fn_param and fn_returns facts are a union and must not be combined as one signature."),
    arg(Fn, "Function entity id.")
  ].

Decl declaration_source_candidate(CheckedDecl, SourceDecl, Method)
  descr [
    extensional(),
    doc("A declaration-map anchor that passed source-file and declaration-context validation. All candidates are retained when ambiguous."),
    arg(CheckedDecl, "Checked declaration entity id."),
    arg(SourceDecl, "Source declaration entity id."),
    arg(Method, "/declaration_map.")
  ].

Decl declaration_source(CheckedDecl, SourceDecl, Method)
  descr [
    extensional(),
    doc("A unique precise checked-to-source declaration link. It never equates the declaration identities."),
    arg(CheckedDecl, "Checked declaration entity id."),
    arg(SourceDecl, "Source declaration entity id."),
    arg(Method, "/declaration_map.")
  ].

Decl declaration_source_outcome(CheckedDecl, Outcome)
  descr [
    extensional(),
    doc("Terminal result of a requested declaration-map attempt. No row means there was no attempt or a unique link succeeded."),
    arg(CheckedDecl, "Checked declaration entity id."),
    arg(Outcome, "/missing_map, /unavailable_map, /malformed_map, /unavailable_source, /unsupported, /unresolved, /ambiguous or /conflict.")
  ].

Decl source_entry(Entry, CheckedEntry, SourceEntry)
  descr [
    extensional(),
    doc("One validated, canonical source-links JSON entry."),
    arg(Entry, "Deterministic configured-entry entity id."),
    arg(CheckedEntry, "Workspace-relative checked entry path."),
    arg(SourceEntry, "Workspace-relative source entry path." )
  ].

Decl source_entry_tsconfig(Entry, Tsconfig)
  descr [
    extensional(),
    doc("Explicit source project configuration selected for an entry."),
    arg(Entry, "Configured-entry entity id."),
    arg(Tsconfig, "Workspace-relative tsconfig path.")
  ].

Decl source_entry_applied(Entry)
  descr [
    extensional(),
    doc("The configured checked path matched a file used by an analyzed program and its binding attempts were evaluated."),
    arg(Entry, "Configured-entry entity id.")
  ].

Decl source_entry_unapplied(Entry)
  descr [
    extensional(),
    doc("The valid checked path did not match any file used by an analyzed program."),
    arg(Entry, "Configured-entry entity id.")
  ].

Decl source_entry_incomplete(Entry, Reason)
  descr [
    extensional(),
    doc("The optional source program or its export inventory is unresolved, unsupported or conflicted."),
    arg(Entry, "Configured-entry entity id."),
    arg(Reason, "/unresolved, /unsupported or /conflict.")
  ].

Decl source_binding_attempt(Assoc, Entry, CheckedFile, Name, Space)
  descr [
    extensional(),
    doc("One configured attempt to associate a checker-visible public binding with the source entry."),
    arg(Assoc, "Deterministic configured binding association id."),
    arg(Entry, "Configured-entry entity id."),
    arg(CheckedFile, "Canonical actual checked file identity."),
    arg(Name, "Public binding name."),
    arg(Space, "/type or /value.")
  ].

Decl source_binding(Assoc, SourceFile, SourceName, Space, Method)
  descr [
    extensional(),
    doc("A configured binding association. The source public name and space must match; this is not declaration equality or build freshness evidence."),
    arg(Assoc, "Configured binding association id."),
    arg(SourceFile, "Canonical source entry file identity."),
    arg(SourceName, "Matched source public name."),
    arg(Space, "/type or /value."),
    arg(Method, "/source_entry.")
  ].

Decl source_binding_checked_decl(Assoc, Declaration)
  descr [
    extensional(),
    doc("Membership in the independently observed checked declaration set for this binding."),
    arg(Assoc, "Configured binding association id."),
    arg(Declaration, "Checked declaration entity id.")
  ].

Decl source_binding_source_decl(Assoc, Declaration)
  descr [
    extensional(),
    doc("Membership in the independently observed source declaration set for this binding."),
    arg(Assoc, "Configured binding association id."),
    arg(Declaration, "Source declaration entity id.")
  ].

Decl source_binding_incomplete(Assoc, Reason)
  descr [
    extensional(),
    doc("The source binding set is unresolved, unsupported or conflicted. Guard this before claiming a coherent association."),
    arg(Assoc, "Configured binding association id."),
    arg(Reason, "/unresolved, /unsupported or /conflict.")
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

Decl global_ref(Site, Name, File)
  descr [
    extensional(),
    doc("A reference to a global value: an identifier that resolves to the global binding with its name, from the default library, an @types package or a declaration file, or a member of globalThis, window, self or global with that name. A local binding with the same name is not a global reference. Type positions are not references."),
    arg(Site, "Site entity id."),
    arg(Name, "Global name, for example \"crypto\" or \"process\"."),
    arg(File, "Path of the file that contains the reference.")
  ].

Decl module_directive(Dir, File, Text)
  descr [
    extensional(),
    doc("A directive in the prologue of a file, for example \"use client\" or \"use server\"."),
    arg(Dir, "Directive entity id."),
    arg(File, "Path of the file."),
    arg(Text, "Directive text without quotes.")
  ].

Decl module_parse_error(Err, File, Message)
  descr [
    extensional(),
    doc("The file has a syntax error. Only the first error of a file has a row. Facts for the file can be incomplete."),
    arg(Err, "Parse error entity id."),
    arg(File, "Path of the file."),
    arg(Message, "Text of the first syntax error.")
  ].

Decl config_adjusted(Config, Option, Action)
  descr [
    extensional(),
    doc("lintai changed a compiler option that TypeScript 7 rejects before loading this tsconfig. The analysis uses the adjusted configuration, which can differ from the project's own build."),
    arg(Config, "Workspace-relative path of the original tsconfig."),
    arg(Option, "Compiler option name."),
    arg(Action, "/removed, /rewritten or /replaced.")
  ].

Decl site_in_fn(Site, Fn)
  descr [
    extensional(),
    doc("The lexical function owner of a call, external-call, unresolved-call, function-reference, ambient-use, global-ref or meta-access site. Module and unsupported sites have no row."),
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
