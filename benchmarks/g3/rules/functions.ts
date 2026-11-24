import { dl, rule } from "@lintai/sdk";
export default rule("benchmark.functions")
  .version(1)
  .program(dl`
    Decl violation(Fn, Name).
    violation(Fn, Name) :- ts.fn(Fn, File, Name).
  `)
  .report("violation")
  .message((row) => row.Name);
