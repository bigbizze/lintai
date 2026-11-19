import { dl, rule } from "@lintai/sdk";

// Geometry coordinate tuples must be readonly and have no optional or rest elements.
export default rule("arch.coordinate-tuples")
	.version(1)
	.program(dl`
		Decl coordinate(Fn, Name, Type).
		coordinate(Fn, Name, Type) :- ts.fn(Fn, File, Name), ts.fn_exported(Fn),
			lintai.glob(File, "src/geometry/**"), ts.fn_signature(Fn, Sig), ts.signature_complete(Sig),
			ts.signature_param(Sig, 0, Type), ts.type_kind(Type, /tuple).
		Decl optional_element(Fn, Name, Position).
		optional_element(Fn, Name, Position) :- coordinate(Fn, Name, Type), ts.type_arg(Type, Position, _),
			ts.type_arg_optional(Type, Position).
		Decl rest_element(Fn, Name, Position).
		rest_element(Fn, Name, Position) :- coordinate(Fn, Name, Type), ts.type_arg(Type, Position, _),
			ts.type_arg_rest(Type, Position).
		Decl mutable_tuple(Fn, Name).
		mutable_tuple(Fn, Name) :- coordinate(Fn, Name, Type), !ts.type_readonly(Type).
	`)
	.report("optional_element", "rest_element", "mutable_tuple")
	.expectNonEmpty("coordinate")
	.message(() => "Coordinate tuples must be readonly and have no optional or rest elements");
