# TypeScript producer base relations and reporting roles.

Decl module(Module)
  descr [
    extensional(),
    doc("One entity per selected primary file."),
    arg(Module, "Module"),
    arg_role(Module, "entity"),
    arg_report_support(Module, "required")
  ].

Decl module_path(Module, File)
  descr [
    extensional(),
    doc("The primary module’s canonical file path."),
    arg(Module, "Module"),
    arg_role(Module, "entity"),
    arg_report_support(Module, "required"),
    arg(File, "File"),
    arg_role(File, "path")
  ].

Decl fn(Fn, File, Name)
  descr [
    extensional(),
    doc("Collected implementation/ownership function in a primary file; display name is not identity."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required"),
    arg(File, "File"),
    arg_role(File, "path"),
    arg(Name, "Name")
  ].

Decl fn_kind(Fn, Kind)
  descr [
    extensional(),
    doc("One syntax kind: /function, /function_expression, /arrow_function, /method, /constructor, /get_accessor or /set_accessor."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl fn_container(Fn, Name)
  descr [
    extensional(),
    doc("Known containing class/object display name; absent when unnamed."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required"),
    arg(Name, "Name")
  ].

Decl fn_exported(Fn)
  descr [
    extensional(),
    doc("Syntactically exported collected function."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl fn_async(Fn)
  descr [
    extensional(),
    doc("Function has the async modifier; this is not a statement about every promise-returning function."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl site(Site, File)
  descr [
    extensional(),
    doc("Primary syntax-event population defined in section 7.2."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(File, "File"),
    arg_role(File, "path")
  ].

Decl site_owner(Site, Owner)
  descr [
    extensional(),
    doc("Unique representable lexical/execution owner: collected function or primary module."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Owner, "Owner"),
    arg_role(Owner, "entity"),
    arg_report_support(Owner, "required")
  ].

Decl site_attribution_unsupported(Site, Reason)
  descr [
    extensional(),
    doc("No representable owner; /instance_construction or /unsupported_context."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl suspension_site(Site, Kind)
  descr [
    extensional(),
    doc("Ordinary /await, /for_await or /await_using syntax; not proof of execution."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl meta_access(Site, AccessPath)
  descr [
    extensional(),
    doc("Observed import.meta member-access path."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(AccessPath, "AccessPath")
  ].

Decl ambient_source(Source, Capability)
  descr [
    extensional(),
    doc("Registered ambient-source/capability pair, independent of observed use."),
    arg(Source, "Source"),
    arg_representation(Source, "name"),
    arg(Capability, "Capability"),
    arg_representation(Capability, "name")
  ].

Decl ambient_use(Site, Source, Capability)
  descr [
    extensional(),
    doc("Use matching a registered ambient source. File/owner are obtained from source-site relations."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Source, "Source"),
    arg_representation(Source, "name"),
    arg(Capability, "Capability"),
    arg_representation(Capability, "name")
  ].

Decl ambient_key(Site, Key)
  descr [
    extensional(),
    doc("Static key used by an ambient read/write."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Key, "Key")
  ].

Decl ambient_dynamic(Site)
  descr [
    extensional(),
    doc("Ambient key is dynamic or the relevant ambient object escapes."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl ambient_write(Site)
  descr [
    extensional(),
    doc("Ambient use assigns to or deletes a keyed member."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl global_ref(Site, Name)
  descr [
    extensional(),
    doc("Value reference to the resolved global binding; same-spelled local variables do not qualify."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Name, "Name")
  ].

Decl module_directive(Site, Text)
  descr [
    extensional(),
    doc("Directive-prologue string in a primary module; no quotation marks in Text."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Text, "Text")
  ].

Decl module_parse_error(Site, Message)
  descr [
    extensional(),
    doc("First syntax diagnostic for a primary file. Other source facts can be incomplete while it exists."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Message, "Message")
  ].

Decl config_adjusted(Config, Option, Action)
  descr [
    extensional(),
    doc("Option adaptation in the fixed applicable-context domain; /removed, /rewritten or /replaced."),
    arg(Config, "Config"),
    arg_role(Config, "path"),
    arg(Option, "Option"),
    arg(Action, "Action"),
    arg_representation(Action, "name")
  ].

Decl import_edge(Site, From, To, Specifier)
  descr [
    extensional(),
    doc("String-literal module reference. From is source path; To is resolved workspace path for internal edges, otherwise specifier/origin data. To is never a module entity ID."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(From, "From"),
    arg_role(From, "path"),
    arg(To, "To"),
    arg(Specifier, "Specifier")
  ].

Decl import_form(Site, Form)
  descr [
    extensional(),
    doc("/static, /reexport, /dynamic, /require, /import_equals or /import_type."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Form, "Form"),
    arg_representation(Form, "name")
  ].

Decl import_internal(Site)
  descr [
    extensional(),
    doc("Resolves to a workspace file outside node_modules; target need not be a primary module."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl import_unresolved(Site)
  descr [
    extensional(),
    doc("No resolved file or declared module under an observed context; not a missing collection result."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl import_package(Site, Pkg)
  descr [
    extensional(),
    doc("Resolved package name, including workspace packages when applicable."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Pkg, "Package")
  ].

Decl import_type_only(Site)
  descr [
    extensional(),
    doc("Reference is exclusively type-oriented under the defined syntax classification."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl import_retained(Site)
  descr [
    extensional(),
    doc("All-type named bindings whose statement is retained as a runtime load by verbatimModuleSyntax."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl import_symbol(Site, Name, Kind)
  descr [
    extensional(),
    doc("Introduced binding; Kind is /default, /namespace or /named. Side-effect imports have no binding rows."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Name, "Name"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl import_symbol_type_only(Site, Name)
  descr [
    extensional(),
    doc("Named binding uses the type modifier."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Name, "Name")
  ].

Decl import_opaque(Site)
  descr [
    extensional(),
    doc("Dynamic import/require with a nonliteral specifier; no invented resolved edge."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl type_ref(Site, Name)
  descr [
    extensional(),
    doc("Supported source type-reference syntax; not the expanded type graph."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Name, "Name")
  ].

Decl type_ref_target(Site, TargetFile)
  descr [
    extensional(),
    doc("Known workspace file declaring the referenced type."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(TargetFile, "TargetFile"),
    arg_role(TargetFile, "path")
  ].

Decl call_site(Site)
  descr [
    extensional(),
    doc("Every supported primary call/new/tagged-template invocation; does not include an argument merely because it is callable."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl call_edge(Site, Callee)
  descr [
    extensional(),
    doc("Observed call to a collected implementation. Caller is site_owner, not part of this row."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Callee, "Callee"),
    arg_role(Callee, "entity"),
    arg_report_support(Callee, "required")
  ].

Decl external_call(Site, OriginKind, Origin, Name)
  descr [
    extensional(),
    doc("Observed external declared target; origin kinds are defined in section 9.1."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(OriginKind, "OriginKind"),
    arg_representation(OriginKind, "name"),
    arg(Origin, "Origin"),
    arg(Name, "Name")
  ].

Decl call_unresolved(Site)
  descr [
    extensional(),
    doc("An observed invocation cannot be followed to a retained implementation or supported external target."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl call_target_conflict(Site)
  descr [
    extensional(),
    doc("Relevant runtime-classification/target observations disagree; retain their positive evidence."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl call_declaration(Site, Declaration)
  descr [
    extensional(),
    doc("Concrete signature declaration selected by a checked context, independent of runtime implementation resolution."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional")
  ].

Decl call_declaration_incomplete(Site, Reason)
  descr [
    extensional(),
    doc("Selected declaration/member evidence is /unresolved, /synthetic, /unsupported or /conflict."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl declaration_member(Declaration, OwnerDeclaration, Name)
  descr [
    extensional(),
    doc("Supported lexical named member and declaring owner; not a claim about the receiver’s runtime implementation."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(OwnerDeclaration, "OwnerDeclaration"),
    arg_role(OwnerDeclaration, "entity"),
    arg_report_support(OwnerDeclaration, "optional"),
    arg(Name, "Name")
  ].

Decl fn_ref(Site, Target)
  descr [
    extensional(),
    doc("Collected function used as a value. It is not necessarily invoked. Owner comes from site_owner."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Target, "Target"),
    arg_role(Target, "entity"),
    arg_report_support(Target, "required")
  ].

Decl fn_value_escape(Site, Kind)
  descr [
    extensional(),
    doc("Supported direct function-valued argument to external/unresolved invocation; only /argument exists."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl fn_value_escape_target(Site, Target)
  descr [
    extensional(),
    doc("Definite collected implementation identified in a transfer observation."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required"),
    arg(Target, "Target"),
    arg_role(Target, "entity"),
    arg_report_support(Target, "required")
  ].

Decl fn_value_escape_unknown(Site)
  descr [
    extensional(),
    doc("At least one transfer observation could not establish a definite collected implementation."),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "required")
  ].

Decl fn_signature(Fn, Signature)
  descr [
    extensional(),
    doc("Whole observed signature belonging to that collected function."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required"),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required")
  ].

Decl signature_arity(Signature, Count)
  descr [
    extensional(),
    doc("Number of parameter slots, including unresolved slots."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Count, "Count"),
    arg_representation(Count, "integer")
  ].

Decl signature_param(Signature, Position, Type)
  descr [
    extensional(),
    doc("Known parameter type at a slot in that signature observation."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Position, "Position"),
    arg_representation(Position, "integer"),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].

Decl signature_param_optional(Signature, Position)
  descr [
    extensional(),
    doc("The slot is known optional in that observation."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl signature_param_rest(Signature, Position)
  descr [
    extensional(),
    doc("The slot is known to be a rest parameter."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl signature_returns(Signature, Type)
  descr [
    extensional(),
    doc("Known return type of that observation."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].

Decl signature_complete(Signature)
  descr [
    extensional(),
    doc("All signature slots, marks and return were established; structural type completeness is separate."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required")
  ].

Decl signature_incomplete(Signature, Reason)
  descr [
    extensional(),
    doc("Whole observation is /unresolved or /unsupported; never coexists with signature_complete for the same ID."),
    arg(Signature, "Signature"),
    arg_role(Signature, "entity"),
    arg_report_support(Signature, "required"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl fn_signature_omitted(Fn)
  descr [
    extensional(),
    doc("Fixed ownership-only omission policy, not selective-work omission."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl fn_signature_unresolved(Fn)
  descr [
    extensional(),
    doc("At least one attempted observation lacked a complete usable signature."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl fn_signature_conflict(Fn)
  descr [
    extensional(),
    doc("Two complete whole signatures for this function disagree."),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl type_kind(Type, Kind)
  descr [
    extensional(),
    doc("Exactly one kind per published type; vocabulary follows section 7.4 and the list after this inventory."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl type_named(Type, Origin, Name)
  descr [
    extensional(),
    doc("Named type metadata; arguments and declaration identity distinguish instantiations. Text is not an identity key by itself."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Origin, "Origin"),
    arg(Name, "Name")
  ].

Decl type_origin_kind(Type, Kind)
  descr [
    extensional(),
    doc("/lib, /package, /ambient_module, /declaration_file or /source_file."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl type_arg(Type, Position, Argument)
  descr [
    extensional(),
    doc("Named type argument or tuple element. Does not include the checker’s synthetic this argument."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer"),
    arg(Argument, "Argument"),
    arg_role(Argument, "entity"),
    arg_report_support(Argument, "optional")
  ].

Decl type_arg_optional(Type, Position)
  descr [
    extensional(),
    doc("Optional tuple element."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl type_arg_rest(Type, Position)
  descr [
    extensional(),
    doc("Rest tuple element."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl type_readonly(Type)
  descr [
    extensional(),
    doc("Readonly tuple; a readonly array is represented by its named library type."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].

Decl type_member(Type, Member)
  descr [
    extensional(),
    doc("Unordered union/intersection membership."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Member, "Member"),
    arg_role(Member, "entity"),
    arg_report_support(Member, "optional")
  ].

Decl type_literal(Type, Kind, Value)
  descr [
    extensional(),
    doc("Literal kind /string, /number, /bigint or /boolean plus payload. A bigint decimal string is not confused with an ordinary string literal."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name"),
    arg(Value, "Value"),
    arg_representation(Value, "string"),
    arg_representation(Value, "integer"),
    arg_representation(Value, "float"),
    arg_representation(Value, "name")
  ].

Decl type_param_name(Type, Name)
  descr [
    extensional(),
    doc("Descriptive type-parameter name; declaration identity distinguishes parameters."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Name, "Name")
  ].

Decl type_prop(Type, Name, PropertyType)
  descr [
    extensional(),
    doc("Represented anonymous-object property; omitted class/interface members are explicitly marked."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Name, "Name"),
    arg(PropertyType, "PropertyType"),
    arg_role(PropertyType, "entity"),
    arg_report_support(PropertyType, "optional")
  ].

Decl type_prop_optional(Type, Name)
  descr [
    extensional(),
    doc("Known optional property in represented structure."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Name, "Name")
  ].

Decl type_prop_readonly(Type, Name)
  descr [
    extensional(),
    doc("Known readonly property in represented structure."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Name, "Name")
  ].

Decl type_sig_param(Type, Position, ParameterType)
  descr [
    extensional(),
    doc("Parameter of a represented structural /function type, not a function-declaration signature observation."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer"),
    arg(ParameterType, "ParameterType"),
    arg_role(ParameterType, "entity"),
    arg_report_support(ParameterType, "optional")
  ].

Decl type_sig_param_optional(Type, Position)
  descr [
    extensional(),
    doc("Known optional structural function-type parameter."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl type_sig_param_rest(Type, Position)
  descr [
    extensional(),
    doc("Known rest structural function-type parameter."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Position, "Position"),
    arg_representation(Position, "integer")
  ].

Decl type_sig_returns(Type, ReturnType)
  descr [
    extensional(),
    doc("Return of a represented structural /function type."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(ReturnType, "ReturnType"),
    arg_role(ReturnType, "entity"),
    arg_report_support(ReturnType, "optional")
  ].

Decl type_incomplete(Type, Reason)
  descr [
    extensional(),
    doc("/truncated, /unsupported, /signatures, /constraint, /conflict or /members_omitted."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl type_display(Type, Text)
  descr [
    extensional(),
    doc("Canonical presentation candidate under section 7.4; not a supported semantic matcher."),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional"),
    arg(Text, "Text")
  ].

Decl declaration(Declaration, Name, Kind)
  descr [
    extensional(),
    doc("Endpoint union in section 7.3, with one canonical whole metadata observation."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Name, "Name"),
    arg(Kind, "Kind"),
    arg_representation(Kind, "name")
  ].

Decl declaration_origin(Declaration, OriginKind, Origin)
  descr [
    extensional(),
    doc("Descriptive origin; never substitutes an assumed source path for a checked declaration."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(OriginKind, "OriginKind"),
    arg_representation(OriginKind, "name"),
    arg(Origin, "Origin")
  ].

Decl declaration_metadata_incomplete(Declaration, Reason)
  descr [
    extensional(),
    doc("Identity-associated metadata is /unresolved, /unsupported or /conflict; no type/map claim."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl declaration_type_subject(Declaration, Space)
  descr [
    extensional(),
    doc("Attempted exported-declaration type domain; /type or /value."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Space, "Space"),
    arg_representation(Space, "name")
  ].

Decl declaration_type(Declaration, Space, Type)
  descr [
    extensional(),
    doc("Known type association for a declaration-type subject."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Space, "Space"),
    arg_representation(Space, "name"),
    arg(Type, "Type"),
    arg_role(Type, "entity"),
    arg_report_support(Type, "optional")
  ].

Decl declaration_type_incomplete(Declaration, Space, Reason)
  descr [
    extensional(),
    doc("Subject-specific /unresolved, /unsupported or /conflict evidence."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Space, "Space"),
    arg_representation(Space, "name"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl declaration_fn(Declaration, Fn)
  descr [
    extensional(),
    doc("Observed direct correspondence to a collected implementation."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Fn, "Fn"),
    arg_role(Fn, "entity"),
    arg_report_support(Fn, "required")
  ].

Decl declaration_fn_incomplete(Declaration, Reason)
  descr [
    extensional(),
    doc("Attempted direct function association is /unresolved, /unsupported or /conflict."),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl module_export(Export, Module, Name, Space)
  descr [
    extensional(),
    doc("Public binding of a primary module in /type or /value space; module is an entity, not a path."),
    arg(Export, "Export"),
    arg_role(Export, "entity"),
    arg_report_support(Export, "required"),
    arg(Module, "Module"),
    arg_role(Module, "entity"),
    arg_report_support(Module, "required"),
    arg(Name, "Name"),
    arg(Space, "Space"),
    arg_representation(Space, "name")
  ].

Decl export_target(Export, Declaration)
  descr [
    extensional(),
    doc("Known endpoint of a binding; overload/merged endpoints form a set, not pairwise identities."),
    arg(Export, "Export"),
    arg_role(Export, "entity"),
    arg_report_support(Export, "required"),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional")
  ].

Decl export_via(Export, Site)
  descr [
    extensional(),
    doc("Syntax site contributing to the binding; a dependency site can have a physical location without being primary."),
    arg(Export, "Export"),
    arg_role(Export, "entity"),
    arg_report_support(Export, "required"),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "optional")
  ].

Decl export_reexport(Export, Site, SourceModule, SourceName, Space)
  descr [
    extensional(),
    doc("Immediate binding-specific re-export. SourceModule remains canonical path data; `*` marks star/namespace names."),
    arg(Export, "Export"),
    arg_role(Export, "entity"),
    arg_report_support(Export, "required"),
    arg(Site, "Site"),
    arg_role(Site, "entity"),
    arg_report_support(Site, "optional"),
    arg(SourceModule, "SourceModule"),
    arg_role(SourceModule, "path"),
    arg(SourceName, "SourceName"),
    arg(Space, "Space"),
    arg_representation(Space, "name")
  ].

Decl export_incomplete(Export, Reason)
  descr [
    extensional(),
    doc("Binding target evidence is /unresolved, /unsupported or /conflict; known positives remain."),
    arg(Export, "Export"),
    arg_role(Export, "entity"),
    arg_report_support(Export, "required"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl module_exports_incomplete(Module, Reason)
  descr [
    extensional(),
    doc("Public-name inventory incomplete/conflicted; propagates through wildcard-export dependencies."),
    arg(Module, "Module"),
    arg_role(Module, "entity"),
    arg_report_support(Module, "required"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl declaration_source_candidate(Checked, Source, Method)
  descr [
    extensional(),
    doc("Validated map candidate; /declaration_map. Ambiguity retains candidates."),
    arg(Checked, "Checked"),
    arg_role(Checked, "entity"),
    arg_report_support(Checked, "optional"),
    arg(Source, "Source"),
    arg_role(Source, "entity"),
    arg_report_support(Source, "optional"),
    arg(Method, "Method"),
    arg_representation(Method, "name")
  ].

Decl declaration_source(Checked, Source, Method)
  descr [
    extensional(),
    doc("Unique nonconflicting precise map link; identities stay distinct."),
    arg(Checked, "Checked"),
    arg_role(Checked, "entity"),
    arg_report_support(Checked, "optional"),
    arg(Source, "Source"),
    arg_role(Source, "entity"),
    arg_report_support(Source, "optional"),
    arg(Method, "Method"),
    arg_representation(Method, "name")
  ].

Decl declaration_source_outcome(Checked, Outcome)
  descr [
    extensional(),
    doc("Terminal failed/uncertain requested map attempt; outcome vocabulary follows below."),
    arg(Checked, "Checked"),
    arg_role(Checked, "entity"),
    arg_report_support(Checked, "optional"),
    arg(Outcome, "Outcome"),
    arg_representation(Outcome, "name")
  ].

Decl source_entry(Entry, CheckedEntry, SourceEntry)
  descr [
    extensional(),
    doc("Validated configured source-links entry. No fabricated source range for this configuration entity."),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity"),
    arg(CheckedEntry, "CheckedEntry"),
    arg_role(CheckedEntry, "path"),
    arg(SourceEntry, "SourceEntry"),
    arg_role(SourceEntry, "path")
  ].

Decl source_entry_tsconfig(Entry, Config)
  descr [
    extensional(),
    doc("Explicit source configuration chosen for the entry."),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity"),
    arg(Config, "Config"),
    arg_role(Config, "path")
  ].

Decl source_entry_applied(Entry)
  descr [
    extensional(),
    doc("The checked entry matched an analyzed compiler input and its binding attempts were considered."),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity")
  ].

Decl source_entry_unapplied(Entry)
  descr [
    extensional(),
    doc("The valid checked entry matched no analyzed compiler input; exactly one applied/unapplied state after reconciliation."),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity")
  ].

Decl source_entry_incomplete(Entry, Reason)
  descr [
    extensional(),
    doc("Optional source program/inventory is /unresolved, /unsupported or /conflict."),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl source_binding_attempt(Binding, Entry, CheckedFile, Name, Space)
  descr [
    extensional(),
    doc("Attempt to associate a checked public binding, in /type or /value space."),
    arg(Binding, "Binding"),
    arg_role(Binding, "entity"),
    arg(Entry, "Entry"),
    arg_role(Entry, "entity"),
    arg(CheckedFile, "CheckedFile"),
    arg_role(CheckedFile, "path"),
    arg(Name, "Name"),
    arg(Space, "Space"),
    arg_representation(Space, "name")
  ].

Decl source_binding(Binding, SourceFile, SourceName, Space, Method)
  descr [
    extensional(),
    doc("Configured binding correspondence by /source_entry; does not assert declaration identity or build freshness."),
    arg(Binding, "Binding"),
    arg_role(Binding, "entity"),
    arg(SourceFile, "SourceFile"),
    arg_role(SourceFile, "path"),
    arg(SourceName, "SourceName"),
    arg(Space, "Space"),
    arg_representation(Space, "name"),
    arg(Method, "Method"),
    arg_representation(Method, "name")
  ].

Decl source_binding_checked_decl(Binding, Declaration)
  descr [
    extensional(),
    doc("Member of the independently observed checked declaration set."),
    arg(Binding, "Binding"),
    arg_role(Binding, "entity"),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional")
  ].

Decl source_binding_source_decl(Binding, Declaration)
  descr [
    extensional(),
    doc("Member of the independently observed source declaration set."),
    arg(Binding, "Binding"),
    arg_role(Binding, "entity"),
    arg(Declaration, "Declaration"),
    arg_role(Declaration, "entity"),
    arg_report_support(Declaration, "optional")
  ].

Decl source_binding_incomplete(Binding, Reason)
  descr [
    extensional(),
    doc("Association is /unresolved, /unsupported or /conflict; positives do not erase uncertainty."),
    arg(Binding, "Binding"),
    arg_role(Binding, "entity"),
    arg(Reason, "Reason"),
    arg_representation(Reason, "name")
  ].

Decl location(Entity, File, StartLine, StartColumn, EndLine, EndColumn)
  descr [
    extensional(),
    doc("All physically locatable published entities in the full configured TypeScript domain. No implicit request during reporting."),
    arg(Entity, "Entity"),
    arg_role(Entity, "entity"),
    arg_report_support(Entity, "required"),
    arg(File, "File"),
    arg_role(File, "path"),
    arg(StartLine, "StartLine"),
    arg_representation(StartLine, "integer"),
    arg(StartColumn, "StartColumn"),
    arg_representation(StartColumn, "integer"),
    arg(EndLine, "EndLine"),
    arg_representation(EndLine, "integer"),
    arg(EndColumn, "EndColumn"),
    arg_representation(EndColumn, "integer")
  ].
